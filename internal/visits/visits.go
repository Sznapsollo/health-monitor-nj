// Package visits keeps the history of who had the monitor open: one row per
// browser tab or wall screen, from when it opened to when it closed, however
// often its connection dropped and came back in between.
package visits

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"time"
)

const schema = `
CREATE TABLE IF NOT EXISTS viewer_visit (
    id         TEXT PRIMARY KEY,
    tab        TEXT    NOT NULL,
    login      TEXT    NOT NULL,
    kind       TEXT    NOT NULL,
    ip         TEXT    NOT NULL,
    user_agent TEXT    NOT NULL,
    platform   TEXT    NOT NULL,
    signals    TEXT    NOT NULL,
    started    INTEGER NOT NULL,
    last_seen  INTEGER NOT NULL,
    ended      INTEGER NOT NULL DEFAULT 0,
    reconnects INTEGER NOT NULL DEFAULT 0,
    sent       INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS viewer_visit_by_start ON viewer_visit (started);
`

// Gap is how long a tab may be away and still continue its visit when it
// reconnects: a network blip, a laptop waking up, the monitor restarting.
const Gap = 5 * time.Minute

// Who is the viewer behind a connection.
type Who struct {
	Tab       string
	Login     string
	Kind      string
	IP        string
	UserAgent string
	Platform  string
}

// Visit is one row of the history.
type Visit struct {
	ID         string    `json:"id"`
	Login      string    `json:"login,omitempty"`
	Kind       string    `json:"kind,omitempty"`
	IP         string    `json:"ip,omitempty"`
	UserAgent  string    `json:"userAgent,omitempty"`
	Platform   string    `json:"platform,omitempty"`
	Signals    []string  `json:"signals"`
	Started    time.Time `json:"started"`
	LastSeen   time.Time `json:"lastSeen"`
	Ended      time.Time `json:"ended,omitzero"`
	Reconnects int       `json:"reconnects"`
	Sent       int64     `json:"sent"`
	// Open says a connection of this visit is live right now.
	Open bool `json:"open"`
}

type visit struct {
	Visit
	tab  string
	live int
}

// Tracker opens, continues and closes visits.
type Tracker struct {
	db  *sql.DB
	now func() time.Time

	mu     sync.Mutex
	byID   map[string]*visit
	recent map[string]*visit
}

// Open prepares the table and takes back the visits a restart interrupted,
// so a tab that reconnects within Gap carries on where it was.
func Open(ctx context.Context, db *sql.DB, now func() time.Time) (*Tracker, error) {
	if now == nil {
		now = time.Now
	}
	if _, err := db.ExecContext(ctx, schema); err != nil {
		return nil, fmt.Errorf("visits: schema: %w", err)
	}
	t := &Tracker{db: db, now: now, byID: map[string]*visit{}, recent: map[string]*visit{}}
	cut := now().Add(-Gap).UnixMilli()
	rows, err := db.QueryContext(ctx, `SELECT `+columns+` FROM viewer_visit WHERE ended = 0 OR ended >= ?`, cut)
	if err != nil {
		return nil, fmt.Errorf("visits: load: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		v, tab, err := scan(rows)
		if err != nil {
			return nil, err
		}
		if v.Ended.IsZero() {
			v.Ended = v.LastSeen
		}
		t.byID[v.ID] = &visit{Visit: v, tab: tab}
		t.recent[tab] = t.byID[v.ID]
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if _, err := db.ExecContext(ctx, `UPDATE viewer_visit SET ended = last_seen WHERE ended = 0`); err != nil {
		return nil, fmt.Errorf("visits: close interrupted: %w", err)
	}
	return t, nil
}

// Connect starts a visit for a new connection, or continues the tab's last
// one when it was away for less than Gap. It returns the visit's id.
func (t *Tracker) Connect(ctx context.Context, who Who) string {
	now := t.now()
	t.mu.Lock()
	v := t.recent[who.Tab]
	continuing := who.Tab != "" && v != nil && v.live == 0 && now.Sub(v.LastSeen) < Gap
	if continuing {
		v.Reconnects++
		v.Ended = time.Time{}
	} else {
		v = &visit{tab: who.Tab, Visit: Visit{
			ID: newID(), Login: who.Login, Kind: who.Kind, IP: who.IP, UserAgent: who.UserAgent,
			Platform: who.Platform, Started: now,
		}}
		t.byID[v.ID] = v
		if who.Tab != "" {
			t.recent[who.Tab] = v
		}
	}
	v.live++
	v.LastSeen = now
	row := v.Visit
	t.mu.Unlock()

	if continuing {
		_, _ = t.db.ExecContext(ctx, `UPDATE viewer_visit SET reconnects = ?, last_seen = ?, ended = 0 WHERE id = ?`,
			row.Reconnects, now.UnixMilli(), row.ID)
		return row.ID
	}
	signals, _ := json.Marshal([]string{})
	_, _ = t.db.ExecContext(ctx, `INSERT INTO viewer_visit
		(id, tab, login, kind, ip, user_agent, platform, signals, started, last_seen)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		row.ID, who.Tab, row.Login, row.Kind, row.IP, row.UserAgent, row.Platform, string(signals),
		now.UnixMilli(), now.UnixMilli())
	return row.ID
}

// Disconnect ends one connection of a visit, adding what it was sent.
func (t *Tracker) Disconnect(ctx context.Context, id string, sent int64, signals []string) {
	now := t.now()
	t.mu.Lock()
	v, ok := t.byID[id]
	if !ok {
		t.mu.Unlock()
		return
	}
	v.live--
	v.Sent += sent
	v.LastSeen = now
	v.Signals = merge(v.Signals, signals)
	if v.live <= 0 {
		v.live = 0
		v.Ended = now
	}
	row := v.Visit
	t.forgetOld(now)
	t.mu.Unlock()

	encoded, _ := json.Marshal(row.Signals)
	_, _ = t.db.ExecContext(ctx, `UPDATE viewer_visit SET sent = ?, signals = ?, last_seen = ?, ended = ? WHERE id = ?`,
		row.Sent, string(encoded), now.UnixMilli(), millis(row.Ended), row.ID)
}

// Seen writes the last-seen time of every open visit, so a crash loses at
// most one interval of them.
func (t *Tracker) Seen(ctx context.Context) {
	now := t.now()
	t.mu.Lock()
	var open []string
	for id, v := range t.byID {
		if v.live > 0 {
			v.LastSeen = now
			open = append(open, id)
		}
	}
	t.mu.Unlock()
	for _, id := range open {
		_, _ = t.db.ExecContext(ctx, `UPDATE viewer_visit SET last_seen = ? WHERE id = ?`, now.UnixMilli(), id)
	}
}

// SeenEvery runs Seen until ctx is cancelled.
func (t *Tracker) SeenEvery(ctx context.Context, every time.Duration) {
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			t.Seen(ctx)
		}
	}
}

// PurgeEvery deletes visits that ended more than keep ago, once at start and
// then every interval, until ctx is cancelled.
func (t *Tracker) PurgeEvery(ctx context.Context, keep, every time.Duration, done func(int64, error)) {
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		done(t.Purge(ctx, t.now().Add(-keep)))
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// forgetOld drops closed visits too old to be continued; callers hold mu.
func (t *Tracker) forgetOld(now time.Time) {
	for id, v := range t.byID {
		if v.live == 0 && now.Sub(v.LastSeen) > Gap {
			delete(t.byID, id)
			if t.recent[v.tab] == v {
				delete(t.recent, v.tab)
			}
		}
	}
}

const columns = `id, tab, login, kind, ip, user_agent, platform, signals, started, last_seen, ended, reconnects, sent`

func scan(rows *sql.Rows) (Visit, string, error) {
	var (
		v                    Visit
		tab, signals         string
		started, seen, ended int64
	)
	if err := rows.Scan(&v.ID, &tab, &v.Login, &v.Kind, &v.IP, &v.UserAgent, &v.Platform, &signals,
		&started, &seen, &ended, &v.Reconnects, &v.Sent); err != nil {
		return Visit{}, "", fmt.Errorf("visits: scan: %w", err)
	}
	_ = json.Unmarshal([]byte(signals), &v.Signals)
	if v.Signals == nil {
		v.Signals = []string{}
	}
	v.Started, v.LastSeen = time.UnixMilli(started), time.UnixMilli(seen)
	if ended > 0 {
		v.Ended = time.UnixMilli(ended)
	}
	return v, tab, nil
}

// Day lists the visits that were open at any time during one UTC day
// (YYYYMMDD), most recent first.
func (t *Tracker) Day(ctx context.Context, day string) ([]Visit, error) {
	from, err := time.Parse("20060102", day)
	if err != nil {
		return nil, fmt.Errorf("visits: %q is not a day", day)
	}
	to := from.Add(24 * time.Hour)
	rows, err := t.db.QueryContext(ctx, `SELECT `+columns+` FROM viewer_visit
		WHERE started < ? AND (ended = 0 OR ended >= ?) ORDER BY started DESC`, to.UnixMilli(), from.UnixMilli())
	if err != nil {
		return nil, fmt.Errorf("visits: list: %w", err)
	}
	defer func() { _ = rows.Close() }()
	t.mu.Lock()
	defer t.mu.Unlock()
	out := []Visit{}
	for rows.Next() {
		v, _, err := scan(rows)
		if err != nil {
			return nil, err
		}
		if live, ok := t.byID[v.ID]; ok && live.live > 0 {
			v.Open, v.Ended = true, time.Time{}
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// Days lists the UTC days that have visits, newest first.
func (t *Tracker) Days(ctx context.Context) ([]string, error) {
	rows, err := t.db.QueryContext(ctx, `SELECT DISTINCT strftime('%Y%m%d', started / 1000, 'unixepoch') FROM viewer_visit ORDER BY 1 DESC`)
	if err != nil {
		return nil, fmt.Errorf("visits: days: %w", err)
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

// Purge deletes visits that ended before the cutoff.
func (t *Tracker) Purge(ctx context.Context, before time.Time) (int64, error) {
	res, err := t.db.ExecContext(ctx, `DELETE FROM viewer_visit WHERE ended > 0 AND ended < ?`, before.UnixMilli())
	if err != nil {
		return 0, fmt.Errorf("visits: purge: %w", err)
	}
	return res.RowsAffected()
}

func merge(have, more []string) []string {
	out := append([]string{}, have...)
	for _, s := range more {
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

func millis(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "v-" + hex.EncodeToString(b)
}
