// Package silence is how an alert gets switched off on purpose: snoozed,
// muted or forgotten, always with who, when, why and until when, and always
// still visible.
package silence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Kind is how a silence behaves.
type Kind string

const (
	// KindSnooze expires by itself at Until.
	KindSnooze Kind = "snooze"
	// KindMute lasts until someone removes it, and is reviewed after 30 days.
	KindMute Kind = "mute"
)

// ReviewAfter is how long an indefinite mute may sit before it is reported as
// possibly forgotten.
const ReviewAfter = 30 * 24 * time.Hour

// Silence is one deliberate silence.
type Silence struct {
	ID       string    `json:"id"`
	Platform string    `json:"platform"`
	Target   string    `json:"target"`
	Kind     Kind      `json:"kind"`
	Reason   string    `json:"reason"`
	By       string    `json:"by"`
	Created  time.Time `json:"created"`
	Until    time.Time `json:"until,omitzero"`
	// Suppressed counts what this silence has hidden, so its cost is visible.
	Suppressed int64 `json:"suppressed"`
}

// Active reports whether the silence applies at t.
func (s Silence) Active(t time.Time) bool {
	switch s.Kind {
	case KindSnooze:
		return !s.Until.IsZero() && t.Before(s.Until)
	case KindMute:
		return true
	}
	return false
}

// NeedsReview reports an indefinite mute old enough to be worth a reminder.
func (s Silence) NeedsReview(t time.Time) bool {
	return s.Kind == KindMute && t.Sub(s.Created) > ReviewAfter
}

const schema = `
CREATE TABLE IF NOT EXISTS silence (
    id         TEXT PRIMARY KEY,
    platform   TEXT    NOT NULL,
    target     TEXT    NOT NULL,
    kind       TEXT    NOT NULL,
    reason     TEXT    NOT NULL,
    by_whom    TEXT    NOT NULL,
    created    INTEGER NOT NULL,
    until      INTEGER NOT NULL,
    suppressed INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS silence_by_platform ON silence (platform, target);
`

// Store keeps the silences, in memory for the matcher and in SQLite so they
// are shared by every viewer and survive a restart.
type Store struct {
	db  *sql.DB
	now func() time.Time

	mu       sync.RWMutex
	byID     map[string]*Silence
	sequence int64

	// Suppressed counts not yet written; flushed by run, never on the reader.
	pending  map[string]int64
	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once
}

const flushEvery = time.Second

// Open prepares the table and loads what is already there.
func Open(db *sql.DB, now func() time.Time) (*Store, error) {
	if now == nil {
		now = time.Now
	}
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("silence: schema: %w", err)
	}
	s := &Store{
		db: db, now: now, byID: map[string]*Silence{},
		pending: map[string]int64{}, stop: make(chan struct{}), done: make(chan struct{}),
	}
	if err := s.reload(context.Background()); err != nil {
		return nil, err
	}
	go s.run()
	return s, nil
}

func (s *Store) Close() error {
	s.stopOnce.Do(func() { close(s.stop) })
	<-s.done
	return nil
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
	s.mu.Lock()
	batch := s.pending
	s.pending = map[string]int64{}
	s.mu.Unlock()
	if len(batch) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		s.requeue(batch)
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for id, n := range batch {
		if _, err := tx.ExecContext(ctx,
			`UPDATE silence SET suppressed = suppressed + ? WHERE id = ?`, n, id); err != nil {
			s.requeue(batch)
			return fmt.Errorf("silence: count: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		s.requeue(batch)
		return err
	}
	return nil
}

func (s *Store) requeue(batch map[string]int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, n := range batch {
		if _, ok := s.byID[id]; ok {
			s.pending[id] += n
		}
	}
}

func (s *Store) reload(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, platform, target, kind, reason, by_whom, created, until, suppressed FROM silence`)
	if err != nil {
		return fmt.Errorf("silence: load: %w", err)
	}
	defer func() { _ = rows.Close() }()

	loaded := map[string]*Silence{}
	for rows.Next() {
		var (
			sil            Silence
			created, until int64
		)
		if err := rows.Scan(&sil.ID, &sil.Platform, &sil.Target, &sil.Kind, &sil.Reason,
			&sil.By, &created, &until, &sil.Suppressed); err != nil {
			return fmt.Errorf("silence: scan: %w", err)
		}
		sil.Created = time.UnixMilli(created)
		if until > 0 {
			sil.Until = time.UnixMilli(until)
		}
		loaded[sil.ID] = &sil
	}
	if err := rows.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.byID = loaded
	return nil
}

// Create records a silence. A reason is required: a silence nobody can
// explain is the one that outlives its cause.
func (s *Store) Create(ctx context.Context, in Silence) (Silence, error) {
	if err := validate(in); err != nil {
		return Silence{}, err
	}

	now := s.now()
	in.Created = now
	s.mu.Lock()
	s.sequence++
	in.ID = fmt.Sprintf("sil-%d-%d", now.UnixMilli(), s.sequence)
	s.mu.Unlock()

	until := int64(0)
	if !in.Until.IsZero() {
		until = in.Until.UnixMilli()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO silence (id, platform, target, kind, reason, by_whom, created, until, suppressed)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0)`,
		in.ID, in.Platform, in.Target, string(in.Kind), in.Reason, in.By, in.Created.UnixMilli(), until)
	if err != nil {
		return Silence{}, fmt.Errorf("silence: insert: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	stored := in
	s.byID[in.ID] = &stored
	return stored, nil
}

// ErrNotFound is returned for a silence that does not exist.
var ErrNotFound = errors.New("silence: no such silence")

// Update changes what a silence covers, how long it lasts and why. Its id,
// platform, creation and suppressed count stay, so its history is kept.
func (s *Store) Update(ctx context.Context, id string, in Silence) (Silence, error) {
	if err := validate(in); err != nil {
		return Silence{}, err
	}
	s.mu.RLock()
	have, ok := s.byID[id]
	var next Silence
	if ok {
		next = *have
	}
	s.mu.RUnlock()
	if !ok {
		return Silence{}, ErrNotFound
	}
	next.Target, next.Kind, next.Reason, next.Until = in.Target, in.Kind, in.Reason, in.Until
	if in.Kind == KindMute {
		next.Until = time.Time{}
	}
	if strings.TrimSpace(in.By) != "" {
		next.By = in.By
	}

	until := int64(0)
	if !next.Until.IsZero() {
		until = next.Until.UnixMilli()
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE silence SET target = ?, kind = ?, reason = ?, by_whom = ?, until = ? WHERE id = ?`,
		next.Target, string(next.Kind), next.Reason, next.By, until, id); err != nil {
		return Silence{}, fmt.Errorf("silence: update: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if cur, ok := s.byID[id]; ok {
		next.Suppressed = cur.Suppressed
		*cur = next
	}
	return next, nil
}

func validate(in Silence) error {
	if strings.TrimSpace(in.Reason) == "" {
		return fmt.Errorf("silence: a reason is required")
	}
	if strings.TrimSpace(in.Target) == "" {
		return fmt.Errorf("silence: a target is required")
	}
	switch in.Kind {
	case KindSnooze:
		if in.Until.IsZero() {
			return fmt.Errorf("silence: a snooze needs a time to expire")
		}
	case KindMute:
	default:
		return fmt.Errorf("silence: unknown kind %q", in.Kind)
	}
	return nil
}

// Delete removes a silence, which is the one-click un-silence in the
// Maintenance panel.
func (s *Store) Delete(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM silence WHERE id = ?`, id); err != nil {
		return fmt.Errorf("silence: delete: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.byID, id)
	delete(s.pending, id)
	return nil
}

// List returns the silences of a platform, newest first. Expired snoozes are
// dropped as they are found, so the list is what is actually in force.
func (s *Store) List(ctx context.Context, platform string) []Silence {
	now := s.now()
	var expired []string

	s.mu.RLock()
	out := make([]Silence, 0, len(s.byID))
	for _, sil := range s.byID {
		if platform != "" && sil.Platform != platform && sil.Platform != "" {
			continue
		}
		if !sil.Active(now) {
			expired = append(expired, sil.ID)
			continue
		}
		out = append(out, *sil)
	}
	s.mu.RUnlock()

	for _, id := range expired {
		_ = s.Delete(ctx, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out
}

// Match reports the silence covering any of these targets, if one is in force.
// Callers pass every target an alert could be addressed by, most specific
// first.
func (s *Store) Match(platform string, targets ...string) (Silence, bool) {
	now := s.now()
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, target := range targets {
		if target == "" {
			continue
		}
		for _, sil := range s.byID {
			if sil.Platform != "" && sil.Platform != platform {
				continue
			}
			if sil.Target != target || !sil.Active(now) {
				continue
			}
			return *sil, true
		}
	}
	return Silence{}, false
}

// Suppressed records that a silence hid something, so the Maintenance panel
// can show what it has been costing. It never waits on the database.
func (s *Store) Suppressed(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sil, ok := s.byID[id]; ok {
		sil.Suppressed++
		s.pending[id]++
	}
}

// NeedingReview lists indefinite mutes old enough to be worth a reminder.
func (s *Store) NeedingReview() []Silence {
	now := s.now()
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Silence
	for _, sil := range s.byID {
		if sil.NeedsReview(now) {
			out = append(out, *sil)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.Before(out[j].Created) })
	return out
}

// Targets builds the ids an alert may be silenced by, most specific first.
func Targets(platform, signal, entity, rule, message string) []string {
	var out []string
	if signal != "" && entity != "" {
		out = append(out, "status:"+signal+"/"+entity)
	}
	if rule != "" {
		out = append(out, "rule:"+rule)
	}
	if signal != "" {
		out = append(out, "signal:"+signal)
	}
	if platform != "" {
		out = append(out, "platform:"+platform)
	}
	return out
}

// MatchTarget is the id of a message-pattern silence.
func MatchTarget(contains string) string {
	return `match:contains="` + contains + `"`
}

// MatchesMessage reports whether any message-pattern silence covers a message.
func (s *Store) MatchesMessage(platform, message string) (Silence, bool) {
	now := s.now()
	lower := strings.ToLower(message)
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, sil := range s.byID {
		if sil.Platform != "" && sil.Platform != platform {
			continue
		}
		if !strings.HasPrefix(sil.Target, `match:contains="`) || !sil.Active(now) {
			continue
		}
		needle := strings.TrimSuffix(strings.TrimPrefix(sil.Target, `match:contains="`), `"`)
		if needle != "" && strings.Contains(lower, strings.ToLower(needle)) {
			return *sil, true
		}
	}
	return Silence{}, false
}
