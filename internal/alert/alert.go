// Package alert holds the alerts the server has raised: the live list every
// viewer sees, the grouping that keeps a storm to one row, and the filters the
// alerts panel asks for.
package alert

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/protocol"
)

// Defaults matching the old server's behaviour: repeats sharing a group key
// collapse for a minute, and the live list holds a day.
const (
	DefaultGroupWindow = time.Minute
	DefaultKeep        = 24 * time.Hour
	DefaultMax         = 5000
)

// Alert is one condition worth a human's attention. Repeats are folded into
// Count rather than appearing again, which is what keeps a failing job from
// burying everything else.
type Alert struct {
	ID       string         `json:"id"`
	Platform string         `json:"platform"`
	Signal   string         `json:"signal,omitempty"`
	Level    protocol.Level `json:"level"`
	Category string         `json:"category,omitempty"`
	Message  string         `json:"message"`
	GroupKey string         `json:"groupKey,omitempty"`
	Source   string         `json:"source,omitempty"`
	Count    int64          `json:"count"`
	First    time.Time      `json:"first"`
	Last     time.Time      `json:"last"`
	Data     map[string]any `json:"data,omitempty"`

	// Target is what a silence would address, e.g. "status:servers/web-2".
	Target string `json:"target,omitempty"`
	// Silenced says an alert was recorded but raised no noise, and why.
	Silenced      bool   `json:"silenced,omitempty"`
	SilenceReason string `json:"silenceReason,omitempty"`

	// seen is when the server last took an occurrence in; Last is the
	// sender's clock, which grouping and expiry must not trust.
	seen time.Time
}

func (a *Alert) seenAt() time.Time {
	if a.seen.IsZero() {
		return a.Last
	}
	return a.seen
}

// Store keeps the live alerts per platform.
type Store struct {
	mu          sync.RWMutex
	byPlatform  map[string][]*Alert
	index       map[string]*Alert
	groupWindow time.Duration
	keep        time.Duration
	max         int
	now         func() time.Time
	raised      int64
	grouped     int64
	// nextExpiry spaces the scan for expired alerts out to once a second,
	// rather than once per alert raised.
	nextExpiry time.Time
}

// Options configure the store.
type Options struct {
	GroupWindow time.Duration
	Keep        time.Duration
	Max         int
	Now         func() time.Time
}

// NewStore builds an alert store.
func NewStore(o Options) *Store {
	if o.GroupWindow <= 0 {
		o.GroupWindow = DefaultGroupWindow
	}
	if o.Keep <= 0 {
		o.Keep = DefaultKeep
	}
	if o.Max <= 0 {
		o.Max = DefaultMax
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Store{
		byPlatform:  make(map[string][]*Alert),
		index:       make(map[string]*Alert),
		groupWindow: o.GroupWindow,
		keep:        o.Keep,
		max:         o.Max,
		now:         o.Now,
	}
}

// SetGroupWindow changes how long repeats collapse, which the rules file owns.
func (s *Store) SetGroupWindow(d time.Duration) {
	if d <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.groupWindow = d
}

// Add records an alert. The returned alert is the stored one — which may be an
// existing row with a higher count — and the bool says whether this is new
// noise (worth a sound or a notification) rather than a repeat.
func (s *Store) Add(a Alert) (Alert, bool) {
	now := s.now()
	if a.Last.IsZero() {
		a.Last = now
	}
	if a.First.IsZero() {
		a.First = a.Last
	}
	if a.Count <= 0 {
		a.Count = 1
	}
	if a.Level == "" {
		a.Level = protocol.LevelInfo
	}
	key := groupingKey(a)

	s.mu.Lock()
	defer s.mu.Unlock()
	if !now.Before(s.nextExpiry) {
		s.expireLocked(now)
		s.nextExpiry = now.Add(time.Second)
	}

	if existing, ok := s.index[key]; ok && now.Sub(existing.seenAt()) <= s.groupWindow {
		existing.Count += a.Count
		existing.Last = a.Last
		existing.seen = now
		// The row shows the most recent occurrence, not the first: a group of
		// slow requests should report the worst one seen, not whichever
		// happened to arrive first. Level only ever goes up, so a warning
		// that turns into an error is not hidden by the grouping.
		existing.Message = a.Message
		if a.Data != nil {
			existing.Data = a.Data
		}
		if worse(a.Level, existing.Level) {
			existing.Level = a.Level
		}
		existing.Silenced, existing.SilenceReason = a.Silenced, a.SilenceReason
		s.grouped++
		return *existing, false
	}

	a.ID = newID(key, a.Last)
	a.seen = now
	stored := &a
	s.index[key] = stored
	s.byPlatform[a.Platform] = append(s.byPlatform[a.Platform], stored)
	s.raised++
	s.trimLocked(a.Platform)
	return *stored, true
}

// Remind updates the latest alert of a group however long ago it was raised:
// one more occurrence, the new message, and now as its last time. It reports
// false when the group has no alert left in memory.
func (s *Store) Remind(platform, groupKey, message string) (Alert, bool) {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.index[groupingKey(Alert{Platform: platform, GroupKey: groupKey})]
	if !ok {
		return Alert{}, false
	}
	existing.Count++
	existing.Last = now
	existing.seen = now
	existing.Message = message
	s.grouped++
	return *existing, true
}

// Resilence asks decide whether each of the platform's live alerts should be
// silenced, and why, and returns the ones whose answer changed.
func (s *Store) Resilence(platform string, decide func(Alert) (bool, string)) []Alert {
	s.mu.Lock()
	defer s.mu.Unlock()
	var changed []Alert
	for _, a := range s.byPlatform[platform] {
		silenced, reason := decide(*a)
		if !silenced {
			reason = ""
		}
		if silenced == a.Silenced && reason == a.SilenceReason {
			continue
		}
		a.Silenced, a.SilenceReason = silenced, reason
		changed = append(changed, *a)
	}
	return changed
}

// Restore puts back alerts read from disk after a restart, keeping their IDs
// so later repeats update the same history row.
func (s *Store) Restore(list []Alert) int {
	sorted := append([]Alert(nil), list...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Last.Before(sorted[j].Last) })

	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := s.now().Add(-s.keep)
	restored := 0
	touched := map[string]bool{}
	for _, a := range sorted {
		if a.ID == "" || a.Last.Before(cutoff) {
			continue
		}
		stored := a
		s.index[groupingKey(stored)] = &stored
		s.byPlatform[stored.Platform] = append(s.byPlatform[stored.Platform], &stored)
		touched[stored.Platform] = true
		restored++
	}
	for platform := range touched {
		s.trimLocked(platform)
	}
	return restored
}

// groupingKey decides what counts as "the same alert". A sender's own group
// key wins; otherwise the category and message do, which is how the old
// server collapsed identical warnings.
func groupingKey(a Alert) string {
	if a.GroupKey != "" {
		return a.Platform + "\x00" + a.GroupKey
	}
	return a.Platform + "\x00" + a.Category + "\x00" + a.Message
}

// severity orders the levels so a group can keep the worst one it has seen.
var severity = map[protocol.Level]int{
	protocol.LevelTrace: 0,
	protocol.LevelDebug: 1,
	protocol.LevelInfo:  2,
	protocol.LevelWarn:  3,
	protocol.LevelError: 4,
}

func worse(a, b protocol.Level) bool { return severity[a] > severity[b] }

func newID(key string, at time.Time) string {
	sum := sha256.Sum256([]byte(key + at.UTC().Format(time.RFC3339Nano)))
	return hex.EncodeToString(sum[:8])
}

func (s *Store) expireLocked(now time.Time) {
	cutoff := now.Add(-s.keep)
	for platform, list := range s.byPlatform {
		kept := list[:0]
		for _, a := range list {
			if a.seenAt().Before(cutoff) {
				s.forgetLocked(a)
				continue
			}
			kept = append(kept, a)
		}
		s.byPlatform[platform] = kept
	}
}

// forgetLocked removes a from the index, unless a newer alert with the same
// grouping key has taken its place there.
func (s *Store) forgetLocked(a *Alert) {
	key := groupingKey(*a)
	if s.index[key] == a {
		delete(s.index, key)
	}
}

func (s *Store) trimLocked(platform string) {
	list := s.byPlatform[platform]
	if len(list) <= s.max {
		return
	}
	dropped := list[:len(list)-s.max]
	for _, a := range dropped {
		s.forgetLocked(a)
	}
	s.byPlatform[platform] = append(list[:0], list[len(list)-s.max:]...)
}

// Filter narrows the alert list, mirroring the old panel's controls.
type Filter struct {
	Levels     []protocol.Level
	Categories []string
	Text       string
	// MinLatencyMS keeps only alerts whose data carries a duration at or
	// above this, which is the old "latency filter".
	MinLatencyMS float64
	Since        time.Time
	Limit        int
	// IncludeSilenced shows what silences have been hiding.
	IncludeSilenced bool
}

// List returns the matching alerts, newest first.
func (s *Store) List(platform string, f Filter) []Alert {
	s.mu.RLock()
	defer s.mu.RUnlock()

	list := s.byPlatform[platform]
	out := make([]Alert, 0, len(list))
	for i := len(list) - 1; i >= 0; i-- {
		a := *list[i]
		if !f.matches(a) {
			continue
		}
		out = append(out, a)
		if f.Limit > 0 && len(out) >= f.Limit {
			break
		}
	}
	return out
}

func (f Filter) matches(a Alert) bool {
	if a.Silenced && !f.IncludeSilenced {
		return false
	}
	if !f.Since.IsZero() && a.Last.Before(f.Since) {
		return false
	}
	if len(f.Levels) > 0 && !containsLevel(f.Levels, a.Level) {
		return false
	}
	if len(f.Categories) > 0 && !containsFold(f.Categories, a.Category) {
		return false
	}
	if f.Text != "" && !matchesText(a, f.Text) {
		return false
	}
	if f.MinLatencyMS > 0 && latencyOf(a) < f.MinLatencyMS {
		return false
	}
	return true
}

// matchesText is the old panel's free-text box: comma-separated terms, any of
// which may appear in the message, category or source.
func matchesText(a Alert, text string) bool {
	haystack := strings.ToLower(a.Message + " " + a.Category + " " + a.Source + " " + a.Signal)
	for _, term := range strings.Split(text, ",") {
		term = strings.TrimSpace(strings.ToLower(term))
		if term == "" {
			continue
		}
		if strings.Contains(haystack, term) {
			return true
		}
	}
	return false
}

func latencyOf(a Alert) float64 {
	for _, key := range []string{"ms", "executionTime", "latencyMs"} {
		if v, ok := a.Data[key]; ok {
			if f, ok := toFloat(v); ok {
				return f
			}
		}
	}
	return 0
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}

func containsLevel(levels []protocol.Level, want protocol.Level) bool {
	for _, l := range levels {
		if l == want {
			return true
		}
	}
	return false
}

func containsFold(list []string, want string) bool {
	for _, s := range list {
		if strings.EqualFold(s, want) {
			return true
		}
	}
	return false
}

// Categories lists the categories seen, sorted, for the panel's checkboxes.
func (s *Store) Categories(platform string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	seen := map[string]bool{}
	for _, a := range s.byPlatform[platform] {
		if a.Category != "" {
			seen[a.Category] = true
		}
	}
	out := make([]string, 0, len(seen))
	for c := range seen {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// Counts summarises the live list by level, for the badges in the top bar.
func (s *Store) Counts(platform string) map[protocol.Level]int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := map[protocol.Level]int64{}
	for _, a := range s.byPlatform[platform] {
		if a.Silenced {
			continue
		}
		out[a.Level]++
	}
	return out
}

// Stats reports how much has been raised and how much folded away.
type Stats struct {
	Raised  int64 `json:"raised"`
	Grouped int64 `json:"grouped"`
	Live    int64 `json:"live"`
}

// Stats summarises the store.
func (s *Store) Stats() Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var live int64
	for _, list := range s.byPlatform {
		live += int64(len(list))
	}
	return Stats{Raised: s.raised, Grouped: s.grouped, Live: live}
}
