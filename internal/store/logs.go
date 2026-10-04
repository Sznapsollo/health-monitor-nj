package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Logs live in one table per day *per kind*, so retention is a DROP TABLE
// rather than a delete scan over millions of rows, and each kind
// expires on its own clock — the bulky dailyLogs need not be kept as long as
// the rest. Every table gets an FTS5 index over its own payload text, which is
// what replaces the old server's linear searchKeyWarnings walk, and keeps one
// kind's vocabulary out of another's index.
const logDayLayout = "20060102"

// dayName is checked before it is ever interpolated into SQL; table names
// cannot be bound as parameters.
var dayNamePattern = regexp.MustCompile(`^\d{8}$`)

// safeKind is a kind that can be a table name as it stands.
var safeKindPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,48}$`)

// hashedSuffix is what kindSuffix produces for a kind that needed spelling
// safely: the hash it ends with means the name cannot be read back from it.
var hashedSuffixPattern = regexp.MustCompile(`_[0-9a-f]{6}$`)

// LogRow is one searchable row: an e-mail sent, a request handled, anything a
// platform wants to be able to look up later.
type LogRow struct {
	Platform string         `json:"platform"`
	Signal   string         `json:"signal"`
	TS       time.Time      `json:"ts"`
	Key      string         `json:"key,omitempty"`
	Account  string         `json:"account,omitempty"`
	User     string         `json:"user,omitempty"`
	URL      string         `json:"url,omitempty"`
	Level    string         `json:"level,omitempty"`
	Payload  map[string]any `json:"payload,omitempty"`
}

// Day is the table a row belongs to.
func (r LogRow) Day() string { return r.TS.UTC().Format(logDayLayout) }

// kindSuffix turns a kind into something that can be part of a table name.
// Ordinary names ("sendLogs") are used as they are, so the tables stay
// readable; anything else is spelled safely and given a hash of the original,
// which keeps two different kinds from landing in the same table.
func kindSuffix(kind string) string {
	if kind == "" {
		return "other"
	}
	if safeKindPattern.MatchString(kind) {
		return kind
	}
	var b strings.Builder
	for _, r := range kind {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	sum := sha256.Sum256([]byte(kind))
	safe := b.String()
	if len(safe) > 32 {
		safe = safe[:32]
	}
	if safe == "" || (safe[0] >= '0' && safe[0] <= '9') {
		safe = "k" + safe
	}
	return safe + "_" + hex.EncodeToString(sum[:3])
}

func logTable(day, suffix string) string    { return "log_" + day + "_" + suffix }
func logFTSTable(day, suffix string) string { return "log_fts_" + day + "_" + suffix }

func validDay(day string) error {
	if !dayNamePattern.MatchString(day) {
		return fmt.Errorf("store: %q is not a day", day)
	}
	return nil
}

func validSuffix(suffix string) error {
	if !safeKindPattern.MatchString(suffix) {
		return fmt.Errorf("store: %q is not a table suffix", suffix)
	}
	return nil
}

// DropLegacyLogTables removes the shape logs had before they were split by
// kind: one table a day holding every kind at once. Per-kind retention cannot
// be applied to a table whose kind is unknowable, so rather than let it linger
// under a rule of its own it goes at start. It returns how many were dropped.
func (s *Store) DropLegacyLogTables(ctx context.Context) (int, error) {
	rows, err := s.read.QueryContext(ctx,
		`SELECT name FROM sqlite_master WHERE type IN ('table','view') AND name LIKE 'log\_%' ESCAPE '\'`)
	if err != nil {
		return 0, fmt.Errorf("store: list log tables: %w", err)
	}
	var legacy []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			_ = rows.Close()
			return 0, err
		}
		// Exactly "log_<day>" or "log_fts_<day>": anything with a kind after
		// the day is the current shape, and FTS5's own shadow tables hang off
		// the index and go with it.
		if day, ok := strings.CutPrefix(name, "log_fts_"); ok && validDay(day) == nil {
			legacy = append(legacy, name)
			continue
		}
		if day, ok := strings.CutPrefix(name, "log_"); ok && validDay(day) == nil {
			legacy = append(legacy, name)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, err
	}
	_ = rows.Close()

	// Indexes first: dropping the content table out from under an external
	// content FTS index leaves it unusable rather than gone.
	sort.Slice(legacy, func(i, j int) bool {
		return strings.HasPrefix(legacy[i], "log_fts_") && !strings.HasPrefix(legacy[j], "log_fts_")
	})
	for _, name := range legacy {
		if _, err := s.db.ExecContext(ctx, `DROP TABLE IF EXISTS `+name); err != nil {
			return 0, fmt.Errorf("store: drop %s: %w", name, err)
		}
	}
	return len(legacy), nil
}

// LogTable is one day of one kind, which is the unit both search and
// retention work in.
type LogTable struct {
	Day string
	// Signal is the kind it holds.
	Signal string
	// Suffix is how the kind is spelled in the table name.
	Suffix string
}

// Name is the table holding the rows.
func (t LogTable) Name() string { return logTable(t.Day, t.Suffix) }

// FTSName is the table holding its search index.
func (t LogTable) FTSName() string { return logFTSTable(t.Day, t.Suffix) }

// ensureLogTable creates one day of one kind the first time a row for it
// arrives, along with its own search index.
func (s *Store) ensureLogTable(ctx context.Context, day, suffix string) error {
	if err := validDay(day); err != nil {
		return err
	}
	if err := validSuffix(suffix); err != nil {
		return err
	}
	key := day + "/" + suffix

	s.logDaysMu.Lock()
	defer s.logDaysMu.Unlock()
	if s.logDays == nil {
		s.logDays = map[string]bool{}
	}
	if s.logDays[key] {
		return nil
	}

	table, fts := logTable(day, suffix), logFTSTable(day, suffix)
	stmts := []string{
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			id       INTEGER PRIMARY KEY AUTOINCREMENT,
			platform TEXT NOT NULL,
			signal   TEXT NOT NULL,
			ts       INTEGER NOT NULL,
			key      TEXT,
			account  TEXT,
			user     TEXT,
			url      TEXT,
			level    TEXT,
			payload  TEXT NOT NULL
		)`, table),
		// Each search filter has an index in ts order, so newest-first with a
		// limit reads only the rows it returns.
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s_by_time ON %s (platform, ts)`, table, table),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s_by_user ON %s (platform, user, ts)`, table, table),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s_by_account ON %s (platform, account, ts)`, table, table),
		// External-content FTS: the index holds no copy of the text, only the
		// terms, and points back at the table by rowid.
		fmt.Sprintf(`CREATE VIRTUAL TABLE IF NOT EXISTS %s USING fts5(
			payload, content='%s', content_rowid='id'
		)`, fts, table),
	}
	for _, stmt := range stmts {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("store: create log table %s: %w", table, err)
		}
	}
	s.logDays[key] = true
	return nil
}

// WriteLogs stores a batch, creating tables as needed. Rows are grouped by the
// day and the kind they belong to, which is one table each.
func (s *Store) WriteLogs(ctx context.Context, rows []LogRow) error {
	_, _, err := s.writeLogsLeft(ctx, rows)
	return err
}

// errLogTableRetiring holds rows back from a table being archived or dropped;
// they are written once it is gone, into a new one.
var errLogTableRetiring = errors.New("store: log table is being dropped")

// writeLogsLeft writes table by table. A busy database stops it and returns
// the rows not yet written, so a retry does not write the others twice; a
// table being dropped has its rows returned too. A table that cannot be
// written at all loses only its own rows, counted in dropped.
func (s *Store) writeLogsLeft(ctx context.Context, rows []LogRow) (left []LogRow, dropped int, err error) {
	if len(rows) == 0 {
		return nil, 0, nil
	}
	type bucket struct{ day, suffix string }
	byTable := map[bucket][]LogRow{}
	var order []bucket
	for _, r := range rows {
		b := bucket{r.Day(), kindSuffix(r.Signal)}
		if _, seen := byTable[b]; !seen {
			order = append(order, b)
		}
		byTable[b] = append(byTable[b], r)
	}

	s.logWriteMu.Lock()
	defer s.logWriteMu.Unlock()
	var retiring, bad error
	for i, b := range order {
		werr := s.writeOneLogTable(ctx, b.day, b.suffix, byTable[b])
		switch {
		case werr == nil:
		case errors.Is(werr, errLogTableRetiring):
			left = append(left, byTable[b]...)
			retiring = werr
		case isBusy(werr):
			for _, rest := range order[i:] {
				left = append(left, byTable[rest]...)
			}
			return left, dropped, errors.Join(werr, bad)
		default:
			dropped += len(byTable[b])
			bad = errors.Join(bad, werr)
		}
	}
	return left, dropped, errors.Join(retiring, bad)
}

func (s *Store) writeOneLogTable(ctx context.Context, day, suffix string, rows []LogRow) error {
	if s.logTableRetiring(day + "/" + suffix) {
		return errLogTableRetiring
	}
	err := s.ensureLogTable(ctx, day, suffix)
	if err == nil {
		err = s.writeLogTable(ctx, day, suffix, rows)
	}
	if !isMissingTable(err) {
		return err
	}
	s.logDaysMu.Lock()
	delete(s.logDays, day+"/"+suffix)
	s.logDaysMu.Unlock()
	if err := s.ensureLogTable(ctx, day, suffix); err != nil {
		return err
	}
	return s.writeLogTable(ctx, day, suffix, rows)
}

func (s *Store) logTableRetiring(key string) bool {
	s.logDaysMu.Lock()
	defer s.logDaysMu.Unlock()
	return s.retiringLogs[key] > 0
}

// retireLogTable keeps the writer away from a table until the returned func
// is called, and forgets that it exists so the next write creates it afresh.
// A write already under way finishes first.
func (s *Store) retireLogTable(t LogTable) func() {
	key := t.Day + "/" + t.Suffix
	s.logWriteMu.Lock()
	s.logDaysMu.Lock()
	if s.retiringLogs == nil {
		s.retiringLogs = map[string]int{}
	}
	s.retiringLogs[key]++
	delete(s.logDays, key)
	s.logDaysMu.Unlock()
	s.logWriteMu.Unlock()
	return func() {
		s.logDaysMu.Lock()
		defer s.logDaysMu.Unlock()
		if s.retiringLogs[key]--; s.retiringLogs[key] <= 0 {
			delete(s.retiringLogs, key)
		}
		delete(s.logDays, key)
	}
}

// isBusy is a write that failed only because another held the database
// longer than the busy timeout: worth trying again.
func isBusy(err error) bool {
	return err != nil && (errors.Is(err, errLogTableRetiring) || strings.Contains(err.Error(), "SQLITE_BUSY") || strings.Contains(err.Error(), "database is locked"))
}

func (s *Store) writeLogTable(ctx context.Context, day, suffix string, rows []LogRow) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	table, fts := logTable(day, suffix), logFTSTable(day, suffix)
	insert, err := tx.PrepareContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (platform, signal, ts, key, account, user, url, level, payload)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, table))
	if err != nil {
		return err
	}
	defer func() { _ = insert.Close() }()

	index, err := tx.PrepareContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (rowid, payload) VALUES (?, ?)`, fts))
	if err != nil {
		return err
	}
	defer func() { _ = index.Close() }()

	for _, r := range rows {
		stored := withoutLifted(r)
		payload, err := json.Marshal(stored)
		if err != nil {
			payload = []byte("{}")
		}
		// The searchable text is the payload's values plus the fields worth
		// finding by name, so "user@example.test" matches whether it is the
		// recipient or the account. Field names are left out: nobody searches
		// for them, and they were a copy of the same words in every row.
		var text strings.Builder
		appendValues(&text, stored)
		for _, v := range []string{r.Key, r.Account, r.User, r.URL} {
			text.WriteByte(' ')
			text.WriteString(v)
		}
		searchable := text.String()

		res, err := insert.ExecContext(ctx, r.Platform, r.Signal, r.TS.UnixMilli(),
			r.Key, r.Account, r.User, r.URL, r.Level, string(payload))
		if err != nil {
			return fmt.Errorf("store: write log: %w", err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		if _, err := index.ExecContext(ctx, id, searchable); err != nil {
			return fmt.Errorf("store: index log: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.stats.wrote(table, len(rows))
	return nil
}

// lifted are the payload fields that also have a column of their own.
var lifted = []struct {
	key    string
	column func(*LogRow) *string
}{
	{"account", func(r *LogRow) *string { return &r.Account }},
	{"user", func(r *LogRow) *string { return &r.User }},
	{"url", func(r *LogRow) *string { return &r.URL }},
}

// withoutLifted is the payload to store: a field whose value is already in its
// column is kept once, there. restoreLifted puts it back on the way out.
func withoutLifted(r LogRow) map[string]any {
	var out map[string]any
	for _, l := range lifted {
		v, ok := r.Payload[l.key].(string)
		if !ok || v == "" || v != *l.column(&r) {
			continue
		}
		if out == nil {
			out = make(map[string]any, len(r.Payload))
			for k, v := range r.Payload {
				out[k] = v
			}
		}
		delete(out, l.key)
	}
	if out == nil {
		return r.Payload
	}
	return out
}

func restoreLifted(r *LogRow) {
	for _, l := range lifted {
		v := *l.column(r)
		if v == "" {
			continue
		}
		if r.Payload == nil {
			r.Payload = map[string]any{}
		}
		if _, ok := r.Payload[l.key]; !ok {
			r.Payload[l.key] = v
		}
	}
}

// appendValues writes every value in a payload, however deep, as search text.
func appendValues(b *strings.Builder, v any) {
	switch v := v.(type) {
	case map[string]any:
		for _, x := range v {
			appendValues(b, x)
		}
	case []any:
		for _, x := range v {
			appendValues(b, x)
		}
	case string:
		b.WriteByte(' ')
		b.WriteString(v)
	case float64:
		b.WriteByte(' ')
		b.WriteString(strconv.FormatFloat(v, 'f', -1, 64))
	case bool:
		b.WriteByte(' ')
		b.WriteString(strconv.FormatBool(v))
	case nil:
	default:
		b.WriteByte(' ')
		fmt.Fprint(b, v)
	}
}

// LogQuery is what the search panel asks for.
type LogQuery struct {
	Platform string
	// Signal narrows to one kind of log, e.g. "sendLogs". It is what makes a
	// search cheap: one table per day rather than every kind's.
	Signal string
	// Text is the free-text search, comma-separated terms, any of which may
	// match.
	Text    string
	Account string
	User    string
	// From and To bound the search; zero means "today".
	From  time.Time
	To    time.Time
	Limit int

	afterID int64
}

// SearchLogs answers the search panel, newest first, across as many tables as
// the window and the chosen kind cover.
func (s *Store) SearchLogs(ctx context.Context, q LogQuery) ([]LogRow, error) {
	if q.Limit <= 0 {
		q.Limit = 500
	}
	tables, err := s.logTablesIn(ctx, q)
	if err != nil {
		return nil, err
	}

	var out []LogRow
	// Newest day first, so a capped search returns the most recent rows. A day
	// may hold several kinds; those are merged before the next day is read, so
	// the order stays honest.
	day := ""
	var batch []LogRow
	flush := func() {
		if len(batch) == 0 {
			return
		}
		sort.SliceStable(batch, func(i, j int) bool { return batch[i].TS.After(batch[j].TS) })
		room := q.Limit - len(out)
		if len(batch) > room {
			batch = batch[:room]
		}
		out = append(out, batch...)
		batch = nil
	}
	for i := len(tables) - 1; i >= 0 && len(out) < q.Limit; i-- {
		if tables[i].Day != day {
			flush()
			day = tables[i].Day
			if len(out) >= q.Limit {
				break
			}
		}
		rows, err := s.searchLogTable(ctx, tables[i], q, q.Limit)
		if err != nil {
			return nil, err
		}
		batch = append(batch, rows...)
	}
	flush()
	return out, nil
}

// ForEachLog streams the window's rows, oldest first, one at a time.
func (s *Store) ForEachLog(ctx context.Context, q LogQuery, fn func(LogRow) error) error {
	tables, err := s.logTablesIn(ctx, q)
	if err != nil {
		return err
	}
	for _, t := range tables {
		if err := s.forEachLogTable(ctx, t, q, fn); err != nil {
			return err
		}
	}
	return nil
}

// logPage is how many rows one read of a download takes. Each page is its own
// short query, so a slow download never holds a read open for minutes, which
// would keep the write-ahead log from being checkpointed.
const logPage = 5000

func (s *Store) forEachLogTable(ctx context.Context, t LogTable, q LogQuery, fn func(LogRow) error) error {
	var after int64
	for {
		page, last, err := s.logPage(ctx, t, q, after)
		if err != nil {
			return err
		}
		for _, r := range page {
			if err := fn(r); err != nil {
				return err
			}
		}
		if len(page) < logPage {
			return nil
		}
		after = last
	}
}

func (s *Store) logPage(ctx context.Context, t LogTable, q LogQuery, after int64) ([]LogRow, int64, error) {
	q.afterID = after
	query, args, err := logTableQuery(t, q, fmt.Sprintf(" ORDER BY %s.id ASC LIMIT %d", t.Name(), logPage))
	if err != nil {
		return nil, 0, err
	}
	rows, err := s.read.QueryContext(ctx, query, args...)
	if err != nil {
		if isMissingTable(err) {
			return nil, 0, nil
		}
		return nil, 0, fmt.Errorf("store: read logs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []LogRow
	var last int64
	for rows.Next() {
		r, id, err := scanLogRow(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, r)
		last = id
	}
	return out, last, rows.Err()
}

func logTableQuery(t LogTable, q LogQuery, order string) (string, []any, error) {
	if err := validDay(t.Day); err != nil {
		return "", nil, err
	}
	if err := validSuffix(t.Suffix); err != nil {
		return "", nil, err
	}
	table := t.Name()

	var (
		where []string
		args  []any
	)
	if q.Platform != "" {
		where = append(where, "platform = ?")
		args = append(args, q.Platform)
	}
	if q.Account != "" {
		where = append(where, "account = ?")
		args = append(args, q.Account)
	}
	if q.User != "" {
		where = append(where, "user = ?")
		args = append(args, q.User)
	}
	if !q.From.IsZero() {
		where = append(where, "ts >= ?")
		args = append(args, q.From.UnixMilli())
	}
	if !q.To.IsZero() {
		where = append(where, "ts <= ?")
		args = append(args, q.To.UnixMilli())
	}
	if q.afterID > 0 {
		where = append(where, table+".id > ?")
		args = append(args, q.afterID)
	}

	from := table
	if match := ftsQuery(q.Text); match != "" {
		// The index narrows the rows before any of the other filters run.
		fts := t.FTSName()
		from = fmt.Sprintf("%s JOIN %s ON %s.rowid = %s.id", fts, table, fts, table)
		where = append([]string{fmt.Sprintf("%s MATCH ?", fts)}, where...)
		args = append([]any{match}, args...)
	}

	query := fmt.Sprintf(`SELECT %s.id, %s.platform, %s.signal, %s.ts, %s.key, %s.account, %s.user, %s.url, %s.level, %s.payload
		FROM %s`, table, table, table, table, table, table, table, table, table, table, from)
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += order
	return query, args, nil
}

func (s *Store) searchLogTable(ctx context.Context, t LogTable, q LogQuery, limit int) ([]LogRow, error) {
	order := " ORDER BY " + t.Name() + ".ts DESC LIMIT ?"
	if ftsQuery(q.Text) != "" {
		// FTS5 hands back matches in rowid order at no cost, and rows go in as
		// they arrive, so this is newest first without sorting every match.
		order = " ORDER BY " + t.FTSName() + ".rowid DESC LIMIT ?"
	}
	query, args, err := logTableQuery(t, q, order)
	if err != nil {
		return nil, err
	}
	args = append(args, limit)

	rows, err := s.read.QueryContext(ctx, query, args...)
	if err != nil {
		// A table that is not there is simply a kind with no logs that day.
		if isMissingTable(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("store: search logs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []LogRow
	for rows.Next() {
		r, _, err := scanLogRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func scanLogRow(rows *sql.Rows) (LogRow, int64, error) {
	var (
		id      int64
		r       LogRow
		ts      int64
		payload string
		key     sql.NullString
		account sql.NullString
		user    sql.NullString
		url     sql.NullString
		level   sql.NullString
	)
	if err := rows.Scan(&id, &r.Platform, &r.Signal, &ts, &key, &account, &user, &url, &level, &payload); err != nil {
		return LogRow{}, 0, fmt.Errorf("store: scan log: %w", err)
	}
	r.TS = time.UnixMilli(ts)
	r.Key, r.Account, r.User = key.String, account.String, user.String
	r.URL, r.Level = url.String, level.String
	_ = json.Unmarshal([]byte(payload), &r.Payload)
	restoreLifted(&r)
	return r, id, nil
}

// ftsQuery turns the panel's comma-separated terms into an FTS5 expression.
// Terms are quoted, so a url or an address cannot be read as FTS syntax.
func ftsQuery(text string) string {
	var terms []string
	for _, part := range strings.Split(text, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		terms = append(terms, `"`+strings.ReplaceAll(part, `"`, `""`)+`"`)
	}
	if len(terms) == 0 {
		return ""
	}
	return strings.Join(terms, " OR ")
}

func isMissingTable(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no such table")
}

// LogTables lists every day-and-kind table on disk, oldest day first. It is
// what both the history list and the retention job work from.
func (s *Store) LogTables(ctx context.Context) ([]LogTable, error) {
	rows, err := s.read.QueryContext(ctx,
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name LIKE 'log\_%' ESCAPE '\' ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("store: list log tables: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []LogTable
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		rest := strings.TrimPrefix(name, "log_")
		if rest == name || strings.HasPrefix(rest, "fts_") {
			continue
		}
		day, suffix, split := strings.Cut(rest, "_")
		if !split || validDay(day) != nil || validSuffix(suffix) != nil {
			continue
		}
		out = append(out, LogTable{Day: day, Suffix: suffix, Signal: s.signalOf(ctx, day, suffix)})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Day != out[j].Day {
			return out[i].Day < out[j].Day
		}
		return out[i].Suffix < out[j].Suffix
	})
	return out, nil
}

// signalOf recovers the kind a table holds. The suffix says it outright unless
// the kind needed spelling safely, in which case the rows themselves do.
func (s *Store) signalOf(ctx context.Context, day, suffix string) string {
	if !hashedSuffixPattern.MatchString(suffix) {
		return suffix
	}
	// The name was spelled safely to become a table, so ask the rows what it
	// really was. A kind that merely looks hashed answers with itself.
	var signal sql.NullString
	err := s.read.QueryRowContext(ctx,
		fmt.Sprintf(`SELECT signal FROM %s LIMIT 1`, logTable(day, suffix))).Scan(&signal)
	if err != nil {
		return ""
	}
	return signal.String
}

// LogDays lists the days that hold logs, oldest first.
func (s *Store) LogDays(ctx context.Context) ([]string, error) {
	tables, err := s.LogTables(ctx)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, t := range tables {
		if seen[t.Day] {
			continue
		}
		seen[t.Day] = true
		out = append(out, t.Day)
	}
	sort.Strings(out)
	return out, nil
}

// logTablesIn narrows the tables to the window, and to one kind when the
// search names one.
func (s *Store) logTablesIn(ctx context.Context, q LogQuery) ([]LogTable, error) {
	tables, err := s.LogTables(ctx)
	if err != nil {
		return nil, err
	}
	wanted := ""
	if q.Signal != "" {
		wanted = kindSuffix(q.Signal)
	}

	var out []LogTable
	for _, t := range tables {
		if wanted != "" && t.Suffix != wanted {
			continue
		}
		d, err := time.Parse(logDayLayout, t.Day)
		if err != nil {
			continue
		}
		if !q.From.IsZero() && d.Add(24*time.Hour).Before(q.From) {
			continue
		}
		if !q.To.IsZero() && d.After(q.To) {
			continue
		}
		out = append(out, t)
	}
	return out, nil
}

// DropLogTable removes one day of one kind, which is how log retention is
// applied: a whole table at a time, never a delete scan.
func (s *Store) DropLogTable(ctx context.Context, t LogTable) error {
	if err := validDay(t.Day); err != nil {
		return err
	}
	if err := validSuffix(t.Suffix); err != nil {
		return err
	}
	defer s.retireLogTable(t)()
	return s.dropLogTable(ctx, t)
}

func (s *Store) dropLogTable(ctx context.Context, t LogTable) error {
	// Emptied a chunk at a time first: dropping a full day in one statement
	// holds the write lock for minutes, and every writer waiting behind it
	// times out.
	for _, table := range []string{t.Name(), t.FTSName() + "_docsize", t.FTSName() + "_data"} {
		if err := s.emptyInChunks(ctx, table); err != nil {
			return err
		}
	}
	for _, stmt := range []string{
		fmt.Sprintf(`DROP TABLE IF EXISTS %s`, t.FTSName()),
		fmt.Sprintf(`DROP TABLE IF EXISTS %s`, t.Name()),
	} {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("store: drop log table %s: %w", t.Name(), err)
		}
	}
	s.forgetLogStats(ctx, t)
	s.forgetActivity(t.Day)
	return nil
}

// dropChunk is how many rows one short transaction deletes when a table is
// emptied before it is dropped.
const dropChunk = 5000

func (s *Store) emptyInChunks(ctx context.Context, table string) error {
	stmt := fmt.Sprintf(`DELETE FROM %s WHERE rowid IN (SELECT rowid FROM %s LIMIT %d)`, table, table, dropChunk)
	for {
		res, err := s.db.ExecContext(ctx, stmt)
		if isMissingTable(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("store: empty %s: %w", table, err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Millisecond):
		}
	}
}

// DropLogDay removes every kind of one day.
func (s *Store) DropLogDay(ctx context.Context, day string) error {
	if err := validDay(day); err != nil {
		return err
	}
	tables, err := s.LogTables(ctx)
	if err != nil {
		return err
	}
	for _, t := range tables {
		if t.Day != day {
			continue
		}
		if err := s.DropLogTable(ctx, t); err != nil {
			return err
		}
	}
	return nil
}

// LogDayCounts is how many rows each day holds across its kinds, for the
// history list, from one listing of the tables.
func (s *Store) LogDayCounts(ctx context.Context) (map[string]int64, error) {
	tables, err := s.LogTables(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]int64)
	for _, t := range tables {
		var n int64
		err := s.read.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM %s`, t.Name())).Scan(&n)
		if isMissingTable(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out[t.Day] += n
	}
	return out, nil
}

// CountLogs is how many rows a day holds across its kinds, for the history
// list.
func (s *Store) CountLogs(ctx context.Context, day string) (int64, error) {
	if err := validDay(day); err != nil {
		return 0, err
	}
	tables, err := s.LogTables(ctx)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, t := range tables {
		if t.Day != day {
			continue
		}
		var n int64
		err := s.read.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM %s`, t.Name())).Scan(&n)
		if isMissingTable(err) {
			continue
		}
		if err != nil {
			return 0, err
		}
		total += n
	}
	return total, nil
}
