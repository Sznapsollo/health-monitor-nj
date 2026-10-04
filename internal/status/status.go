// Package status tracks the things that report they are alive — servers, job
// workers, mail senders — and notices when one goes quiet. It is the old
// server's status maps generalised: three maps became one kind.
package status

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

const (
	flushEvery = time.Second
	maxPending = 10000
)

const schema = `
CREATE TABLE IF NOT EXISTS status (
    platform   TEXT    NOT NULL,
    signal     TEXT    NOT NULL,
    key        TEXT    NOT NULL,
    payload    TEXT    NOT NULL,
    last_seen  INTEGER NOT NULL,
    offline_since INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (platform, signal, key)
) WITHOUT ROWID;
`

// Entity is one thing that reports its state.
type Entity struct {
	Platform string         `json:"platform"`
	Signal   string         `json:"signal"`
	Key      string         `json:"key"`
	Payload  map[string]any `json:"payload,omitempty"`
	LastSeen time.Time      `json:"lastSeen"`
	Offline  bool           `json:"offline"`
	// OfflineSince is when it stopped reporting, zero while it is healthy.
	OfflineSince time.Time `json:"offlineSince,omitzero"`
}

// Target is the id a silence would address this entity by.
func (e Entity) Target() string { return "status:" + e.Signal + "/" + e.Key }

// Age is how long since it last reported.
func (e Entity) Age(now time.Time) time.Duration { return now.Sub(e.LastSeen) }

// Store keeps the entities of every platform, in memory and in SQLite so a
// restart does not forget which servers exist.
type Store struct {
	db  *sql.DB
	now func() time.Time

	mu       sync.RWMutex
	entities map[string]*Entity // keyed platform\x00signal\x00key

	// Latest state per key, written by run; Observe never touches the database.
	pendingMu sync.Mutex
	pending   map[string]Entity
	written   atomic.Int64
	dropped   atomic.Int64
	stop      chan struct{}
	done      chan struct{}
	stopOnce  sync.Once
}

type Stats struct {
	Written int64 `json:"written"`
	Dropped int64 `json:"dropped"`
	Pending int   `json:"pending"`
}

// Open prepares the table and loads what is already known.
func Open(db *sql.DB, now func() time.Time) (*Store, error) {
	if now == nil {
		now = time.Now
	}
	s := &Store{
		db: db, now: now, entities: map[string]*Entity{},
		pending: map[string]Entity{}, stop: make(chan struct{}), done: make(chan struct{}),
	}
	if db == nil {
		close(s.done)
		return s, nil
	}
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("status: schema: %w", err)
	}
	if err := addOfflineSince(db); err != nil {
		return nil, err
	}
	if err := s.load(context.Background()); err != nil {
		return nil, err
	}
	go s.run()
	return s, nil
}

// addOfflineSince brings a table from before offline was remembered up to date.
func addOfflineSince(db *sql.DB) error {
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('status') WHERE name = 'offline_since'`).Scan(&n); err != nil {
		return fmt.Errorf("status: schema: %w", err)
	}
	if n > 0 {
		return nil
	}
	if _, err := db.Exec(`ALTER TABLE status ADD COLUMN offline_since INTEGER NOT NULL DEFAULT 0`); err != nil {
		return fmt.Errorf("status: schema: %w", err)
	}
	return nil
}

func (s *Store) Close() error {
	s.stopOnce.Do(func() { close(s.stop) })
	<-s.done
	return nil
}

func (s *Store) Stats() Stats {
	s.pendingMu.Lock()
	n := len(s.pending)
	s.pendingMu.Unlock()
	return Stats{Written: s.written.Load(), Dropped: s.dropped.Load(), Pending: n}
}

func (s *Store) run() {
	defer close(s.done)
	t := time.NewTicker(flushEvery)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			s.flush()
		case <-s.stop:
			s.flush()
			return
		}
	}
}

func (s *Store) flush() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = s.Flush(ctx)
}

func (s *Store) Flush(ctx context.Context) error {
	if s.db == nil {
		return nil
	}
	s.pendingMu.Lock()
	batch := s.pending
	s.pending = map[string]Entity{}
	s.pendingMu.Unlock()
	if len(batch) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		s.requeue(batch)
		return err
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO status (platform, signal, key, payload, last_seen, offline_since) VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT (platform, signal, key) DO UPDATE SET payload = excluded.payload,
		     last_seen = excluded.last_seen, offline_since = excluded.offline_since`)
	if err != nil {
		s.requeue(batch)
		return err
	}
	defer func() { _ = stmt.Close() }()
	for _, e := range batch {
		payload, err := json.Marshal(e.Payload)
		if err != nil {
			payload = []byte("{}")
		}
		var offlineSince int64
		if e.Offline {
			offlineSince = e.OfflineSince.UnixMilli()
		}
		if _, err := stmt.ExecContext(ctx, e.Platform, e.Signal, e.Key, string(payload), e.LastSeen.UnixMilli(), offlineSince); err != nil {
			s.requeue(batch)
			return fmt.Errorf("status: persist: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		s.requeue(batch)
		return err
	}
	s.written.Add(int64(len(batch)))
	return nil
}

func (s *Store) requeue(batch map[string]Entity) {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	for k, e := range batch {
		if _, newer := s.pending[k]; !newer {
			s.pending[k] = e
		}
	}
}

func (s *Store) load(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT platform, signal, key, payload, last_seen, offline_since FROM status`)
	if err != nil {
		return fmt.Errorf("status: load: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var (
			e            Entity
			payloadJSON  string
			lastSeen     int64
			offlineSince int64
		)
		if err := rows.Scan(&e.Platform, &e.Signal, &e.Key, &payloadJSON, &lastSeen, &offlineSince); err != nil {
			return fmt.Errorf("status: scan: %w", err)
		}
		e.LastSeen = time.UnixMilli(lastSeen)
		if offlineSince > 0 {
			e.Offline, e.OfflineSince = true, time.UnixMilli(offlineSince)
		}
		if payloadJSON != "" {
			_ = json.Unmarshal([]byte(payloadJSON), &e.Payload)
		}
		s.entities[key(e.Platform, e.Signal, e.Key)] = &e
	}
	return rows.Err()
}

func key(platform, signal, entity string) string {
	return platform + "\x00" + signal + "\x00" + entity
}

// Observe records a heartbeat. It reports the entity and whether this is a
// recovery — something that was offline reporting again — which is what
// re-arms an "until recovery" silence.
func (s *Store) Observe(platform, signal, entity string, payload map[string]any, at time.Time) (Entity, bool) {
	if at.IsZero() {
		at = s.now()
	}
	k := key(platform, signal, entity)

	s.mu.Lock()
	e, ok := s.entities[k]
	if !ok {
		e = &Entity{Platform: platform, Signal: signal, Key: entity}
		s.entities[k] = e
	}
	recovered := e.Offline
	e.Payload = payload
	e.LastSeen = at
	e.Offline = false
	e.OfflineSince = time.Time{}
	out := *e
	s.mu.Unlock()

	s.persist(out)
	return out, recovered
}

func (s *Store) persist(e Entity) {
	if s.db == nil {
		return
	}
	k := key(e.Platform, e.Signal, e.Key)
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	if _, have := s.pending[k]; !have && len(s.pending) >= maxPending {
		s.dropped.Add(1)
		return
	}
	s.pending[k] = e
}

// CheckOffline marks entities that have been silent longer than their
// platform allows and returns the ones that have just crossed the line, so
// each is alerted about once.
func (s *Store) CheckOffline(after func(platform string) time.Duration) []Entity {
	now := s.now()
	var newly []Entity

	s.mu.Lock()
	for _, e := range s.entities {
		if e.Offline || now.Sub(e.LastSeen) <= after(e.Platform) {
			continue
		}
		e.Offline = true
		e.OfflineSince = now
		newly = append(newly, *e)
	}
	s.mu.Unlock()
	for _, e := range newly {
		s.persist(e)
	}
	sort.Slice(newly, func(i, j int) bool { return newly[i].Key < newly[j].Key })
	return newly
}

// List returns a platform's entities, offline first then by name, which is the
// order the status table wants.
func (s *Store) List(platform string) []Entity {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Entity, 0, len(s.entities))
	for _, e := range s.entities {
		if platform != "" && e.Platform != platform {
			continue
		}
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Offline != out[j].Offline {
			return out[i].Offline
		}
		if out[i].Signal != out[j].Signal {
			return out[i].Signal < out[j].Signal
		}
		return out[i].Key < out[j].Key
	})
	return out
}

// Forget removes an entity for good, which is what a decommissioned machine
// needs: it stops alerting, and if it ever reports again it reappears as new.
func (s *Store) Forget(ctx context.Context, platform, signal, entity string) error {
	k := key(platform, signal, entity)
	s.mu.Lock()
	delete(s.entities, k)
	s.mu.Unlock()
	s.pendingMu.Lock()
	delete(s.pending, k)
	s.pendingMu.Unlock()

	if s.db == nil {
		return nil
	}
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM status WHERE platform = ? AND signal = ? AND key = ?`, platform, signal, entity)
	if err != nil {
		return fmt.Errorf("status: forget: %w", err)
	}
	return nil
}

// ForgetSignal removes every entity of one signal, as Forget does one.
func (s *Store) ForgetSignal(ctx context.Context, platform, signal string) error {
	s.mu.Lock()
	for k, e := range s.entities {
		if e.Platform == platform && e.Signal == signal {
			delete(s.entities, k)
		}
	}
	s.mu.Unlock()
	s.pendingMu.Lock()
	for k, e := range s.pending {
		if e.Platform == platform && e.Signal == signal {
			delete(s.pending, k)
		}
	}
	s.pendingMu.Unlock()

	if s.db == nil {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM status WHERE platform = ? AND signal = ?`, platform, signal)
	if err != nil {
		return fmt.Errorf("status: forget signal: %w", err)
	}
	return nil
}

// Counts summarises how many entities are up and down, for the badges.
func (s *Store) Counts(platform string) (online, offline int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, e := range s.entities {
		if platform != "" && e.Platform != platform {
			continue
		}
		if e.Offline {
			offline++
		} else {
			online++
		}
	}
	return online, offline
}
