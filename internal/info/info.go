// Package info keeps the reports of info signals: whole packets as they
// arrived, the latest few per sender, to be read in the UI.
package info

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

const schema = `
CREATE TABLE IF NOT EXISTS info_entry (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    platform TEXT    NOT NULL,
    signal   TEXT    NOT NULL,
    key      TEXT    NOT NULL,
    received INTEGER NOT NULL,
    content  TEXT    NOT NULL
);
CREATE INDEX IF NOT EXISTS info_entry_by_key ON info_entry (platform, signal, key, received);
`

// ErrNotFound is a report that is not kept.
var ErrNotFound = errors.New("info: no such report")

// Entry is one sender of one info signal.
type Entry struct {
	Signal   string    `json:"signal"`
	Key      string    `json:"key"`
	Received time.Time `json:"received"`
	Versions int       `json:"versions"`
}

// Version is one kept report.
type Version struct {
	ID       int64     `json:"id"`
	Received time.Time `json:"received"`
	Size     int       `json:"size"`
}

// Store reads and writes the reports.
type Store struct {
	db *sql.DB
	// merging keeps two reports of one sender from folding into the same
	// old record at once.
	merging sync.Mutex
}

// Open prepares the table.
func Open(ctx context.Context, db *sql.DB) (*Store, error) {
	if _, err := db.ExecContext(ctx, schema); err != nil {
		return nil, fmt.Errorf("info: schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Record keeps a report and drops the sender's oldest beyond keep.
func (s *Store) Record(ctx context.Context, platform, signal, key string, content map[string]any, at time.Time, keep int) error {
	body, err := json.Marshal(content)
	if err != nil {
		return fmt.Errorf("info: encode: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO info_entry (platform, signal, key, received, content) VALUES (?, ?, ?, ?, ?)`,
		platform, signal, key, at.UnixMilli(), string(body)); err != nil {
		return fmt.Errorf("info: record: %w", err)
	}
	if keep <= 0 {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM info_entry WHERE platform = ? AND signal = ? AND key = ? AND id NOT IN (
		SELECT id FROM info_entry WHERE platform = ? AND signal = ? AND key = ? ORDER BY received DESC, id DESC LIMIT ?)`,
		platform, signal, key, platform, signal, key, keep); err != nil {
		return fmt.Errorf("info: trim: %w", err)
	}
	return nil
}

// Merge folds a report into the sender's one record: objects are merged
// field by field at every depth, anything else is replaced, and nothing is
// ever removed. keep is ignored; a merged signal keeps one record.
func (s *Store) Merge(ctx context.Context, platform, signal, key string, content map[string]any, at time.Time, _ int) error {
	s.merging.Lock()
	defer s.merging.Unlock()
	merged := map[string]any{}
	_, have, err := s.Content(ctx, platform, signal, key, 0)
	switch {
	case errors.Is(err, ErrNotFound):
	case err != nil:
		return err
	default:
		if err := json.Unmarshal(have, &merged); err != nil || merged == nil {
			merged = map[string]any{}
		}
	}
	return s.Record(ctx, platform, signal, key, mergeInto(merged, content), at, 1)
}

func mergeInto(dst, src map[string]any) map[string]any {
	for k, v := range src {
		in, isMap := v.(map[string]any)
		have, hadMap := dst[k].(map[string]any)
		if isMap && hadMap {
			dst[k] = mergeInto(have, in)
		} else {
			dst[k] = v
		}
	}
	return dst
}

// Entries lists every sender of every info signal of a platform, latest first.
func (s *Store) Entries(ctx context.Context, platform string) ([]Entry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT signal, key, MAX(received), COUNT(*) FROM info_entry
		WHERE platform = ? GROUP BY signal, key ORDER BY signal, key`, platform)
	if err != nil {
		return nil, fmt.Errorf("info: entries: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []Entry{}
	for rows.Next() {
		var (
			e        Entry
			received int64
		)
		if err := rows.Scan(&e.Signal, &e.Key, &received, &e.Versions); err != nil {
			return nil, fmt.Errorf("info: entries: %w", err)
		}
		e.Received = time.UnixMilli(received)
		out = append(out, e)
	}
	return out, rows.Err()
}

// Versions lists the kept reports of one sender, newest first.
func (s *Store) Versions(ctx context.Context, platform, signal, key string) ([]Version, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, received, LENGTH(content) FROM info_entry
		WHERE platform = ? AND signal = ? AND key = ? ORDER BY received DESC, id DESC`, platform, signal, key)
	if err != nil {
		return nil, fmt.Errorf("info: versions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []Version{}
	for rows.Next() {
		var (
			v        Version
			received int64
		)
		if err := rows.Scan(&v.ID, &received, &v.Size); err != nil {
			return nil, fmt.Errorf("info: versions: %w", err)
		}
		v.Received = time.UnixMilli(received)
		out = append(out, v)
	}
	return out, rows.Err()
}

// Content is one kept report of a sender; id 0 is the latest.
func (s *Store) Content(ctx context.Context, platform, signal, key string, id int64) (Version, json.RawMessage, error) {
	query := `SELECT id, received, content FROM info_entry WHERE platform = ? AND signal = ? AND key = ?`
	args := []any{platform, signal, key}
	if id > 0 {
		query += ` AND id = ?`
		args = append(args, id)
	}
	query += ` ORDER BY received DESC, id DESC LIMIT 1`
	var (
		v        Version
		received int64
		content  string
	)
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&v.ID, &received, &content)
	if errors.Is(err, sql.ErrNoRows) {
		return Version{}, nil, ErrNotFound
	}
	if err != nil {
		return Version{}, nil, fmt.Errorf("info: content: %w", err)
	}
	v.Received, v.Size = time.UnixMilli(received), len(content)
	return v, json.RawMessage(content), nil
}

// Signal is one info signal that has reports kept.
type Signal struct {
	Platform string
	Name     string
}

// Signals lists every platform and signal with reports kept.
func (s *Store) Signals(ctx context.Context) ([]Signal, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT platform, signal FROM info_entry`)
	if err != nil {
		return nil, fmt.Errorf("info: signals: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Signal
	for rows.Next() {
		var sig Signal
		if err := rows.Scan(&sig.Platform, &sig.Name); err != nil {
			return nil, fmt.Errorf("info: signals: %w", err)
		}
		out = append(out, sig)
	}
	return out, rows.Err()
}

// Forget deletes every kept report of one sender of a signal.
func (s *Store) Forget(ctx context.Context, platform, signal, key string) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM info_entry WHERE platform = ? AND signal = ? AND key = ?`,
		platform, signal, key)
	if err != nil {
		return 0, fmt.Errorf("info: forget: %w", err)
	}
	return res.RowsAffected()
}

// Purge deletes one signal's reports received before the cutoff.
func (s *Store) Purge(ctx context.Context, platform, signal string, before time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM info_entry WHERE platform = ? AND signal = ? AND received < ?`,
		platform, signal, before.UnixMilli())
	if err != nil {
		return 0, fmt.Errorf("info: purge: %w", err)
	}
	return res.RowsAffected()
}

// PurgeEvery deletes reports older than their signal keeps them, once at
// start and then every interval, until ctx is cancelled.
func (s *Store) PurgeEvery(ctx context.Context, days func(platform, signal string) int, now func() time.Time, every time.Duration, done func(int64, error)) {
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		done(s.purgeAll(ctx, days, now()))
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (s *Store) purgeAll(ctx context.Context, days func(platform, signal string) int, now time.Time) (int64, error) {
	signals, err := s.Signals(ctx)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, sig := range signals {
		n, err := s.Purge(ctx, sig.Platform, sig.Name, now.AddDate(0, 0, -days(sig.Platform, sig.Name)))
		if err != nil {
			return total, err
		}
		total += n
	}
	return total, nil
}
