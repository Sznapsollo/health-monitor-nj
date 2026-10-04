package store

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"sort"
	"time"
)

// A gap longer than this ends a stretch of activity. It is the old server's
// rule from updateUsersDailyStats, kept so the numbers stay comparable.
const activityGap = 10 * time.Minute

const activitySchema = `
CREATE TABLE IF NOT EXISTS user_activity (
    day     TEXT    NOT NULL,
    account TEXT    NOT NULL,
    user    TEXT    NOT NULL,
    minutes INTEGER NOT NULL,
    events  INTEGER NOT NULL,
    PRIMARY KEY (day, account, user)
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS activity_done (
    day TEXT    PRIMARY KEY,
    at  INTEGER NOT NULL
) WITHOUT ROWID;
`

// Activity is how long one person was busy on one day.
type Activity struct {
	Day     string `json:"day"`
	Account string `json:"account"`
	User    string `json:"user"`
	Minutes int64  `json:"minutes"`
	Events  int64  `json:"events"`
}

// activityFresh is how long a computed day is trusted before it is read again.
const activityFresh = 10 * time.Minute

// activityDay is a day's activity as far as its logs have been read: each
// person's stretches, and how far into each log table the reading got, so the
// next pass reads only the rows added since.
type activityDay struct {
	cursor map[string]int64
	people map[activityKey]*activityPerson
}

type activityKey struct{ account, user string }

// activityPerson holds stretches sorted by start, none within activityGap of
// the next, so a late event lands in the right place whatever order rows
// arrive in.
type activityPerson struct {
	events int64
	spans  [][2]int64
}

func (p *activityPerson) add(ts int64) {
	p.events++
	gap := activityGap.Milliseconds()
	i := sort.Search(len(p.spans), func(i int) bool { return p.spans[i][0] > ts })
	if i > 0 && ts <= p.spans[i-1][1] {
		return
	}
	p.spans = slices.Insert(p.spans, i, [2]int64{ts, ts})
	if i+1 < len(p.spans) && p.spans[i+1][0]-ts <= gap {
		p.spans[i][1] = p.spans[i+1][1]
		p.spans = slices.Delete(p.spans, i+1, i+2)
	}
	if i > 0 && ts-p.spans[i-1][1] <= gap {
		p.spans[i-1][1] = p.spans[i][1]
		p.spans = slices.Delete(p.spans, i, i+1)
	}
}

func (p *activityPerson) minutes() int64 {
	var total time.Duration
	for _, sp := range p.spans {
		total += stretch(sp[0], sp[1])
	}
	return int64(total.Minutes())
}

// RefreshActivity brings a day's activity up to date unless it was done in
// the last few minutes, or the day is over and was already done after it
// ended, when nothing can change it any more. It reports whether it did any
// work.
func (s *Store) RefreshActivity(ctx context.Context, day string, now time.Time) (bool, error) {
	did, _, err := s.refreshActivity(ctx, day, now, false)
	return did, err
}

// ComputeActivity brings a day's activity up to date now: per-user minutes
// of activity, where two events closer together than the gap count as one
// continuous stretch and a lone event counts as a minute. Only rows added
// since the last pass are read.
func (s *Store) ComputeActivity(ctx context.Context, day string) (int, error) {
	_, people, err := s.refreshActivity(ctx, day, time.Now(), true)
	return people, err
}

func (s *Store) refreshActivity(ctx context.Context, day string, now time.Time, force bool) (bool, int, error) {
	if err := validDay(day); err != nil {
		return false, 0, err
	}
	end, err := time.Parse(logDayLayout, day)
	if err != nil {
		return false, 0, err
	}
	end = end.Add(24 * time.Hour)
	if _, err := s.db.ExecContext(ctx, activitySchema); err != nil {
		return false, 0, fmt.Errorf("store: activity schema: %w", err)
	}

	s.activityMu.Lock()
	defer s.activityMu.Unlock()
	done, err := s.activityDone(ctx, day)
	if err != nil || done {
		return false, 0, err
	}
	if at, ok := s.activityAt[day]; ok && !force && now.Sub(at) < activityFresh {
		return false, 0, nil
	}

	tables, err := s.LogTables(ctx)
	if err != nil {
		return false, 0, err
	}
	if !slices.ContainsFunc(tables, func(t LogTable) bool { return t.Day == day }) {
		// Its logs were archived or deleted: what was worked out from them stays.
		return false, 0, nil
	}
	if s.activityDays == nil {
		s.activityDays = map[string]*activityDay{}
	}
	d := s.activityDays[day]
	if d == nil {
		d = &activityDay{cursor: map[string]int64{}, people: map[activityKey]*activityPerson{}}
		s.activityDays[day] = d
	}
	for _, t := range tables {
		if t.Day != day {
			continue
		}
		if err := s.readActivity(ctx, t, d); err != nil {
			return false, 0, err
		}
	}
	if err := s.writeActivity(ctx, day, d); err != nil {
		return false, 0, err
	}
	if s.activityAt == nil {
		s.activityAt = map[string]time.Time{}
	}
	s.activityAt[day] = now
	if now.Sub(end) > activityFresh {
		if _, err := s.db.ExecContext(ctx, `INSERT OR REPLACE INTO activity_done (day, at) VALUES (?, ?)`,
			day, now.UnixMilli()); err != nil {
			return true, len(d.people), err
		}
		delete(s.activityDays, day)
	}
	return true, len(d.people), nil
}

// readActivity adds the rows of one table added since the last pass.
func (s *Store) readActivity(ctx context.Context, t LogTable, d *activityDay) error {
	rows, err := s.read.QueryContext(ctx, fmt.Sprintf(
		`SELECT id, account, user, ts FROM %s WHERE id > ? ORDER BY id`, t.Name()), d.cursor[t.Name()])
	if err != nil {
		if isMissingTable(err) {
			return nil
		}
		return fmt.Errorf("store: read day %s: %w", t.Day, err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var (
			id, ts  int64
			account sql.NullString
			user    sql.NullString
		)
		if err := rows.Scan(&id, &account, &user, &ts); err != nil {
			return err
		}
		d.cursor[t.Name()] = id
		if user.String == "" {
			continue
		}
		k := activityKey{account.String, user.String}
		p := d.people[k]
		if p == nil {
			p = &activityPerson{}
			d.people[k] = p
		}
		p.add(ts)
	}
	return rows.Err()
}

func (s *Store) writeActivity(ctx context.Context, day string, d *activityDay) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM user_activity WHERE day = ?`, day); err != nil {
		return err
	}
	insert, err := tx.PrepareContext(ctx,
		`INSERT INTO user_activity (day, account, user, minutes, events) VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer func() { _ = insert.Close() }()
	for k, p := range d.people {
		if _, err := insert.ExecContext(ctx, day, k.account, k.user, p.minutes(), p.events); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// activityDone says a day was brought up to date after it ended; callers
// hold activityMu.
func (s *Store) activityDone(ctx context.Context, day string) (bool, error) {
	var n int
	err := s.read.QueryRowContext(ctx, `SELECT COUNT(*) FROM activity_done WHERE day = ?`, day).Scan(&n)
	if isMissingTable(err) {
		return false, nil
	}
	return n > 0, err
}

// forgetActivity drops what was read of a day, when one of its tables goes:
// the next pass reads what is left from the start.
func (s *Store) forgetActivity(day string) {
	s.activityMu.Lock()
	defer s.activityMu.Unlock()
	delete(s.activityDays, day)
	delete(s.activityAt, day)
}

// stretch is how long one continuous run lasted, counting a single event as a
// minute rather than as nothing.
func stretch(start, end int64) time.Duration {
	d := time.Duration(end-start) * time.Millisecond
	if d < time.Minute {
		return time.Minute
	}
	return d
}

// ActivityFor returns a day's activity, busiest first.
func (s *Store) ActivityFor(ctx context.Context, day string) ([]Activity, error) {
	if _, err := s.db.ExecContext(ctx, activitySchema); err != nil {
		return nil, err
	}
	rows, err := s.read.QueryContext(ctx,
		`SELECT day, account, user, minutes, events FROM user_activity WHERE day = ? ORDER BY minutes DESC`, day)
	if err != nil {
		return nil, fmt.Errorf("store: activity: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Activity
	for rows.Next() {
		var a Activity
		if err := rows.Scan(&a.Day, &a.Account, &a.User, &a.Minutes, &a.Events); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ActivityDays lists the days with activity, newest first.
func (s *Store) ActivityDays(ctx context.Context) ([]string, error) {
	if _, err := s.db.ExecContext(ctx, activitySchema); err != nil {
		return nil, err
	}
	rows, err := s.read.QueryContext(ctx, `SELECT DISTINCT day FROM user_activity ORDER BY day DESC`)
	if err != nil {
		return nil, fmt.Errorf("store: activity days: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var day string
		if err := rows.Scan(&day); err != nil {
			return nil, err
		}
		out = append(out, day)
	}
	return out, rows.Err()
}

// PurgeActivity drops activity rows older than a day.
func (s *Store) PurgeActivity(ctx context.Context, before string) error {
	if _, err := s.db.ExecContext(ctx, activitySchema); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM user_activity WHERE day < ?`, before); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM activity_done WHERE day < ?`, before)
	return err
}

// Backup copies the database to path with VACUUM INTO, which is safe while the
// server is running and is what the backup action uses.
func (s *Store) Backup(ctx context.Context, path string) error {
	if _, err := s.db.ExecContext(ctx, `VACUUM INTO ?`, path); err != nil {
		return fmt.Errorf("store: backup: %w", err)
	}
	return nil
}
