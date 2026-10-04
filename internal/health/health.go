// Package health lists what the monitor itself is having trouble with and
// remembers which of those problems someone has already looked at.
package health

import (
	"database/sql"
	"fmt"
	"sync"
	"time"
)

// Sources of an issue.
const (
	SourceQuarantine = "quarantine"
	SourceCounter    = "counter"
)

// CounterPrefix starts the key of every counter issue.
const CounterPrefix = "counter:"

// Issue is one kind of problem, with how often it happened.
type Issue struct {
	Key        string     `json:"key"`
	Source     string     `json:"source"`
	Reason     string     `json:"reason"`
	Platform   string     `json:"platform,omitempty"`
	Signal     string     `json:"signal,omitempty"`
	Type       string     `json:"type,omitempty"`
	Count      int64      `json:"count"`
	New        int64      `json:"new"`
	First      *time.Time `json:"first,omitempty"`
	Last       *time.Time `json:"last,omitempty"`
	LastSource string     `json:"lastSource,omitempty"`
	Known      bool       `json:"known"`
	KnownBy    string     `json:"knownBy,omitempty"`
	KnownAt    *time.Time `json:"knownAt,omitempty"`
}

type ack struct {
	count int64
	by    string
	at    time.Time
}

// Book keeps the acknowledgements. A quarantine issue marked known stays
// known; a counter issue is known only until its counter grows again.
type Book struct {
	db  *sql.DB
	now func() time.Time

	mu   sync.Mutex
	acks map[string]ack
}

const schema = `
CREATE TABLE IF NOT EXISTS hm_known (
    key   TEXT PRIMARY KEY,
    count INTEGER NOT NULL,
    by    TEXT    NOT NULL,
    at    INTEGER NOT NULL
);`

// Open prepares the table and loads the acknowledgements. Counter
// acknowledgements are dropped: counters restart with the process.
func Open(db *sql.DB, now func() time.Time) (*Book, error) {
	if now == nil {
		now = time.Now
	}
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("health: schema: %w", err)
	}
	if _, err := db.Exec(`DELETE FROM hm_known WHERE key LIKE ?`, CounterPrefix+"%"); err != nil {
		return nil, fmt.Errorf("health: reset counters: %w", err)
	}
	rows, err := db.Query(`SELECT key, count, by, at FROM hm_known`)
	if err != nil {
		return nil, fmt.Errorf("health: load: %w", err)
	}
	defer func() { _ = rows.Close() }()
	b := &Book{db: db, now: now, acks: map[string]ack{}}
	for rows.Next() {
		var key string
		var a ack
		var at int64
		if err := rows.Scan(&key, &a.count, &a.by, &at); err != nil {
			return nil, err
		}
		a.at = time.UnixMilli(at)
		b.acks[key] = a
	}
	return b, rows.Err()
}

// Annotate marks each issue known or new.
func (b *Book) Annotate(issues []Issue) []Issue {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := range issues {
		is := &issues[i]
		is.New, is.Known, is.KnownBy, is.KnownAt = is.Count, false, "", nil
		a, ok := b.acks[is.Key]
		if !ok {
			continue
		}
		at := a.at
		is.KnownBy, is.KnownAt = a.by, &at
		switch {
		case is.Source == SourceQuarantine:
			is.Known, is.New = true, 0
		case is.Count <= a.count:
			is.Known, is.New = true, 0
		default:
			is.New = is.Count - a.count
		}
	}
	return issues
}

// Unknown counts the issues nobody has marked known.
func Unknown(issues []Issue) int {
	n := 0
	for _, is := range issues {
		if !is.Known {
			n++
		}
	}
	return n
}

// Mark records the given issues as known at their current counts, or forgets
// that they were when known is false.
func (b *Book) Mark(issues []Issue, keys []string, known bool, by string) error {
	counts := make(map[string]int64, len(issues))
	for _, is := range issues {
		counts[is.Key] = is.Count
	}
	now := b.now()
	tx, err := b.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	next := map[string]*ack{}
	for _, key := range keys {
		if key == "" {
			return fmt.Errorf("health: bad key %q", key)
		}
		if !known {
			if _, err := tx.Exec(`DELETE FROM hm_known WHERE key = ?`, key); err != nil {
				return err
			}
			next[key] = nil
			continue
		}
		a := ack{count: counts[key], by: by, at: now}
		if _, err := tx.Exec(`INSERT INTO hm_known (key, count, by, at) VALUES (?, ?, ?, ?)
			ON CONFLICT(key) DO UPDATE SET count = excluded.count, by = excluded.by, at = excluded.at`,
			key, a.count, a.by, a.at.UnixMilli()); err != nil {
			return err
		}
		next[key] = &a
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for key, a := range next {
		if a == nil {
			delete(b.acks, key)
		} else {
			b.acks[key] = *a
		}
	}
	return nil
}
