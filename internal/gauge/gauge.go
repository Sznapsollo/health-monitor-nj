// Package gauge holds the latest labelled values a platform reports: queue
// depths, connected users, anything whose current value matters more than its
// history. It is the old server's queueCountersCache and vpnUsersCache
// generalised into one kind.
package gauge

import (
	"sort"
	"strings"
	"sync"
	"time"
)

// DefaultTTL drops a source that has stopped reporting. The old VPN chart used
// twice the sender's cron interval; two minutes is the same idea.
const DefaultTTL = 2 * time.Minute

// Point is one labelled reading.
type Point struct {
	Label string  `json:"label"`
	Value float64 `json:"value"`
	Warn  bool    `json:"warn,omitempty"`
	Unit  string  `json:"unit,omitempty"`
}

// Reading is what one source last said.
type Reading struct {
	Platform string    `json:"platform"`
	Signal   string    `json:"signal"`
	Source   string    `json:"source"`
	At       time.Time `json:"at"`
	Points   []Point   `json:"points"`
}

// View is a signal's current state, merged across the sources still reporting.
type View struct {
	Platform  string    `json:"platform"`
	Signal    string    `json:"signal"`
	Points    []Point   `json:"points"`
	Sources   []string  `json:"sources"`
	UpdatedAt time.Time `json:"updatedAt,omitzero"`
}

// Reports counts what a signal has sent since the process started.
type Reports struct {
	Count      int64
	First      time.Time
	Last       time.Time
	LastSource string
}

// Store keeps the readings.
type Store struct {
	now    func() time.Time
	ttl    time.Duration
	ttlFor func(platform, signal string) time.Duration

	mu       sync.RWMutex
	readings map[string]*Reading // keyed platform\x00signal\x00source
	reports  map[string]*Reports // keyed platform\x00signal
}

// NewStore builds a gauge store. A zero ttl uses the default.
func NewStore(now func() time.Time, ttl time.Duration) *Store {
	if now == nil {
		now = time.Now
	}
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	return &Store{now: now, ttl: ttl, readings: map[string]*Reading{}, reports: map[string]*Reports{}}
}

func (s *Store) SetTTLFor(fn func(platform, signal string) time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ttlFor = fn
}

func (s *Store) ttlOf(platform, signal string) time.Duration {
	if s.ttlFor != nil {
		if d := s.ttlFor(platform, signal); d > 0 {
			return d
		}
	}
	return s.ttl
}

func key(platform, signal, source string) string {
	return platform + "\x00" + signal + "\x00" + source
}

// Observe records what one source reports now, replacing what it said before.
func (s *Store) Observe(platform, signal, source string, points []Point, at time.Time) {
	if at.IsZero() {
		at = s.now()
	}
	if source == "" {
		source = "default"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.readings[key(platform, signal, source)] = &Reading{
		Platform: platform, Signal: signal, Source: source, At: at, Points: points,
	}
	r, ok := s.reports[key(platform, signal, "")]
	if !ok {
		r = &Reports{First: at}
		s.reports[key(platform, signal, "")] = r
	}
	r.Count++
	r.Last, r.LastSource = at, source
}

// ReportsOf says how often a signal has reported since the process started.
func (s *Store) ReportsOf(platform, signal string) Reports {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if r, ok := s.reports[key(platform, signal, "")]; ok {
		return *r
	}
	return Reports{}
}

// View merges every source still inside the TTL. Labels reported by several
// sources are added together, which is how the old VPN chart counted users
// across gateways.
func (s *Store) View(platform, signal string) View {
	now := s.now()
	out := View{Platform: platform, Signal: signal}
	totals := map[string]*Point{}
	var order []string

	s.mu.RLock()
	ttl := s.ttlOf(platform, signal)
	for _, r := range s.readings {
		if r.Platform != platform || r.Signal != signal {
			continue
		}
		if now.Sub(r.At) > ttl {
			continue
		}
		out.Sources = append(out.Sources, r.Source)
		if r.At.After(out.UpdatedAt) {
			out.UpdatedAt = r.At
		}
		for _, p := range r.Points {
			existing, ok := totals[p.Label]
			if !ok {
				copied := p
				totals[p.Label] = &copied
				order = append(order, p.Label)
				continue
			}
			existing.Value += p.Value
			existing.Warn = existing.Warn || p.Warn
		}
	}
	s.mu.RUnlock()

	sort.Strings(out.Sources)
	sort.Strings(order)
	for _, label := range order {
		out.Points = append(out.Points, *totals[label])
	}
	return out
}

// Signals lists the gauge signals a platform is reporting, sorted.
func (s *Store) Signals(platform string) []string {
	now := s.now()
	seen := map[string]bool{}
	s.mu.RLock()
	for _, r := range s.readings {
		if r.Platform == platform && now.Sub(r.At) <= s.ttlOf(platform, r.Signal) {
			seen[r.Signal] = true
		}
	}
	s.mu.RUnlock()

	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Platforms lists every platform with a gauge still reporting, sorted.
func (s *Store) Platforms() []string {
	now := s.now()
	seen := map[string]bool{}
	s.mu.RLock()
	for _, r := range s.readings {
		if now.Sub(r.At) <= s.ttlOf(r.Platform, r.Signal) {
			seen[r.Platform] = true
		}
	}
	s.mu.RUnlock()

	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Forget drops every reading of a signal; it is back with its next report.
func (s *Store) Forget(platform, signal string) {
	prefix := key(platform, signal, "")
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.readings {
		if strings.HasPrefix(k, prefix) {
			delete(s.readings, k)
		}
	}
	delete(s.reports, prefix)
}

// Sweep forgets sources that have stopped reporting, so the map does not grow
// with every instance that ever existed.
func (s *Store) Sweep() int {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	dropped := 0
	for k, r := range s.readings {
		// Kept for a while past the TTL, so a brief gap does not lose the
		// history of which sources exist.
		if now.Sub(r.At) > 10*s.ttlOf(r.Platform, r.Signal) {
			delete(s.readings, k)
			dropped++
		}
	}
	for k, r := range s.reports {
		platform, signal, _ := strings.Cut(strings.TrimSuffix(k, "\x00"), "\x00")
		if now.Sub(r.Last) > 10*s.ttlOf(platform, signal) {
			delete(s.reports, k)
		}
	}
	return dropped
}
