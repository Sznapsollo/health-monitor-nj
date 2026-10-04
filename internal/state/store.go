package state

import (
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/signal"
)

// Sample is one pre-aggregated slice of traffic: everything the readers saw
// for one signal, one minute and one exact set of dimension values.
type Sample struct {
	Platform string
	Signal   string
	Minute   int64
	Dims     map[string]string
	Agg      Agg
}

// Dirty names a minute that changed, which is what the hub turns into an
// update for every session interested in it.
type Dirty struct {
	Platform string
	Signal   string
	Minute   int64
}

// Store is the hot state of every platform. Each platform has its own lock, so
// two platforms never wait for each other; within one platform the work is
// merging a few hundred pre-aggregated samples a second, not one per packet.
type Store struct {
	reg *signal.Registry
	now func() time.Time

	mu        sync.RWMutex
	platforms map[string]*platformState
	// maxHotBytes is the memory guard: when the estimate goes over
	// it, the oldest detail is dropped first. Totals are never evicted for
	// memory, and SQLite still has everything.
	maxHotBytes int64
	evictions   int64
}

type platformState struct {
	mu     sync.RWMutex
	series map[string]*Series
	// pairs remembers what sessions asked for even before any packet for
	// that signal has arrived, so the breakdown starts with the first one.
	pairs map[string][]Pair
}

// NewStore builds hot state fed by the definitions in reg.
func NewStore(reg *signal.Registry, now func() time.Time) *Store {
	if now == nil {
		now = time.Now
	}
	return &Store{reg: reg, now: now, platforms: make(map[string]*platformState)}
}

// lookupPlatform finds a platform without creating one. Reads must use this:
// a chart asking about a platform that does not exist would otherwise invent
// it, and an invented empty name then sorts first in every list.
func (s *Store) lookupPlatform(name string) (*platformState, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.platforms[name]
	return p, ok
}

// platform finds or creates a platform. Only the write paths use it.
func (s *Store) platform(name string) *platformState {
	s.mu.RLock()
	p, ok := s.platforms[name]
	s.mu.RUnlock()
	if ok {
		return p
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.platforms[name]; ok {
		return p
	}
	p = &platformState{series: make(map[string]*Series), pairs: make(map[string][]Pair)}
	s.platforms[name] = p
	return p
}

// series returns the hot state for one signal, creating it from the registry
// the first time a packet for it arrives. It returns nil when the signal is
// not one the server knows, which the caller quarantines.
func (s *Store) series(platform, name string) *Series {
	p := s.platform(platform)
	p.mu.RLock()
	ser, ok := p.series[name]
	p.mu.RUnlock()
	if ok {
		return ser
	}
	def, ok := s.reg.Lookup(platform, name)
	if !ok {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if ser, ok := p.series[name]; ok {
		return ser
	}
	ser = NewSeries(def)
	ser.SetActivePairs(p.pairs[name])
	p.series[name] = ser
	return ser
}

// Apply folds a flush of samples into the cubes and reports which minutes
// changed. Samples for unknown signals are reported in the second result so
// the caller can quarantine them.
func (s *Store) Apply(samples []Sample) (dirty []Dirty, unknown []Sample) {
	now := s.now()
	seen := make(map[Dirty]struct{}, len(samples))

	// Resolve every series first, so the platform lock is taken once and held
	// only for the merging itself.
	type item struct {
		series *Series
		sample Sample
		labels map[string]string
	}
	// Labels come from the registry, not the series, so a catalogue edit
	// applies without a restart.
	labelsOf := map[[2]string]map[string]string{}
	grouped := map[*platformState][]item{}
	for _, sample := range samples {
		ser := s.series(sample.Platform, sample.Signal)
		if ser == nil {
			unknown = append(unknown, sample)
			continue
		}
		key := [2]string{sample.Platform, sample.Signal}
		labels, ok := labelsOf[key]
		if !ok {
			if def, found := s.reg.Lookup(sample.Platform, sample.Signal); found {
				labels = def.Display.Labels
			}
			labelsOf[key] = labels
		}
		p := s.platform(sample.Platform)
		grouped[p] = append(grouped[p], item{series: ser, sample: sample, labels: labels})
	}

	for p, items := range grouped {
		p.mu.Lock()
		for _, it := range items {
			it.series.Observe(it.sample.Minute, it.sample.Dims, it.sample.Agg, now)
			if it.labels != nil {
				it.series.learnLabels(it.labels, it.sample.Dims)
			}
		}
		p.mu.Unlock()
		for _, it := range items {
			d := Dirty{Platform: it.sample.Platform, Signal: it.sample.Signal, Minute: it.sample.Minute}
			if _, ok := seen[d]; !ok {
				seen[d] = struct{}{}
				dirty = append(dirty, d)
			}
		}
	}
	return dirty, unknown
}

// Restore puts stored minutes of one signal back into memory. It reports
// false for a signal the registry does not know.
func (s *Store) Restore(platform, sig string, minutes []RestoredMinute) bool {
	ser := s.series(platform, sig)
	if ser == nil {
		return false
	}
	p := s.platform(platform)
	now := s.now()
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, m := range minutes {
		ser.Restore(m, now)
	}
	ser.Maintain(now)
	return true
}

// RestoreLabels puts names read back from disk into memory. A name learned
// from traffic since the start is kept.
func (s *Store) RestoreLabels(platform, sig string, labels []Label) bool {
	ser := s.series(platform, sig)
	if ser == nil {
		return false
	}
	p := s.platform(platform)
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, l := range labels {
		if ser.Label(l.Dim, l.Value) == "" {
			ser.setLabel(l.Dim, l.Value, l.Name)
		}
	}
	return true
}

// PairsOf is every pair a signal keeps: the ones being watched and the ones
// its definition always keeps. The durable writer stores exactly these.
func (s *Store) PairsOf(platform, sig string) []Pair {
	ser := s.series(platform, sig)
	if ser == nil {
		return nil
	}
	p := s.platform(platform)
	p.mu.RLock()
	defer p.mu.RUnlock()
	return slices.Clone(ser.ActivePairs())
}

// View is what a session should see of one signal right now.
func (s *Store) View(platform, sig string, c Criteria) (View, bool) {
	p, ok := s.lookupPlatform(platform)
	if !ok {
		return View{}, false
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	ser := p.series[sig]
	if ser == nil {
		return View{}, false
	}
	return ser.View(c, s.now()), true
}

// Label is the name learned for one value of a labelled dimension.
func (s *Store) Label(platform, sig, dim, value string) string {
	p, ok := s.lookupPlatform(platform)
	if !ok {
		return ""
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	if ser := p.series[sig]; ser != nil {
		return ser.Label(dim, value)
	}
	return ""
}

// MinuteView is one changed minute, trimmed for a session.
func (s *Store) MinuteView(platform, sig string, c Criteria, minuteKey int64) (View, bool) {
	p, ok := s.lookupPlatform(platform)
	if !ok {
		return View{}, false
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	ser := p.series[sig]
	if ser == nil {
		return View{}, false
	}
	return ser.MinuteView(c, minuteKey, s.now()), true
}

// MinutesView is MinuteView for several minutes of one signal at once.
func (s *Store) MinutesView(platform, sig string, c Criteria, minuteKeys []int64) (View, bool) {
	p, ok := s.lookupPlatform(platform)
	if !ok {
		return View{}, false
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	ser := p.series[sig]
	if ser == nil {
		return View{}, false
	}
	return ser.MinutesView(c, minuteKeys, s.now()), true
}

// SetActivePairs tells one signal which dimension pairs sessions are looking
// at, so only those are precomputed.
func (s *Store) SetActivePairs(platform, sig string, pairs []Pair) {
	p := s.pairsPlatform(platform, sig)
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pairs[sig] = append(p.pairs[sig][:0], pairs...)
	if ser := p.series[sig]; ser != nil {
		ser.SetActivePairs(pairs)
	}
}

// pairsPlatform is where a signal's pairs are kept: only for a signal that is
// defined or already held, so a request naming anything else leaves nothing
// behind.
func (s *Store) pairsPlatform(platform, sig string) *platformState {
	if platform == "" || sig == "" {
		return nil
	}
	if _, ok := s.reg.Lookup(platform, sig); ok {
		return s.platform(platform)
	}
	p, ok := s.lookupPlatform(platform)
	if !ok {
		return nil
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	_, held := p.series[sig]
	_, asked := p.pairs[sig]
	if !held && !asked {
		return nil
	}
	return p
}

// AddActivePair adds one pair without touching the pairs other viewers asked for.
func (s *Store) AddActivePair(platform, sig string, pair Pair) {
	p := s.pairsPlatform(platform, sig)
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, have := range p.pairs[sig] {
		if have == pair {
			return
		}
	}
	p.pairs[sig] = append(p.pairs[sig], pair)
	if ser := p.series[sig]; ser != nil {
		ser.SetActivePairs(p.pairs[sig])
	}
}

// ActivePairSignals lists the signals of a platform that are being kept
// precomputed, so a caller can tell which ones nobody is looking at any more.
func (s *Store) ActivePairSignals(platform string) []string {
	p, ok := s.lookupPlatform(platform)
	if !ok {
		return nil
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]string, 0, len(p.pairs))
	for sig, pairs := range p.pairs {
		if len(pairs) > 0 {
			out = append(out, sig)
		}
	}
	sort.Strings(out)
	return out
}

// SetMaxHotBytes sets the memory guard. Zero switches it off.
func (s *Store) SetMaxHotBytes(n int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.maxHotBytes = n
}

// Maintain applies the retention tiers to every signal, then the memory guard.
func (s *Store) Maintain() {
	now := s.now()
	for _, p := range s.snapshotPlatforms() {
		p.mu.Lock()
		for _, ser := range p.series {
			ser.Maintain(now)
		}
		p.mu.Unlock()
	}
	s.enforceMemory()
}

// EstimatedBytes is roughly how much the hot state is holding.
func (s *Store) EstimatedBytes() int64 {
	var total int64
	for _, p := range s.snapshotPlatforms() {
		p.mu.RLock()
		for _, ser := range p.series {
			total += ser.EstimatedBytes()
		}
		p.mu.RUnlock()
	}
	return total
}

// Evictions is how many times the guard had to drop detail.
func (s *Store) Evictions() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.evictions
}

// enforceMemory demotes the oldest detailed minutes of the largest signals
// until the estimate is back under the limit. Totals stay, so the main chart
// keeps its shape and only the breakdowns fall back to SQLite. It stops at
// nine tenths of the limit, so the next pass does not start over at once.
func (s *Store) enforceMemory() {
	s.mu.RLock()
	limit := s.maxHotBytes
	s.mu.RUnlock()
	if limit <= 0 {
		return
	}
	total := s.EstimatedBytes()
	if total <= limit {
		return
	}

	target := limit / 10 * 9
	var evicted int64
	for total > target {
		freed, ok := s.evictLargest()
		if !ok {
			break
		}
		total -= freed
		evicted++
	}
	s.mu.Lock()
	s.evictions += evicted
	s.mu.Unlock()
}

// evictLargest demotes one minute of whichever signal holds the most among
// those with breakdowns left, and reports how much that freed and whether
// anything was left to evict.
func (s *Store) evictLargest() (int64, bool) {
	var biggest *Series
	var biggestLock *platformState
	var most int64

	for _, p := range s.snapshotPlatforms() {
		p.mu.RLock()
		for _, ser := range p.series {
			if ser.detailKeys == 0 {
				continue
			}
			if n := ser.EstimatedBytes(); n > most {
				most, biggest, biggestLock = n, ser, p
			}
		}
		p.mu.RUnlock()
	}
	if biggest == nil {
		return 0, false
	}
	biggestLock.mu.Lock()
	defer biggestLock.mu.Unlock()
	before := biggest.EstimatedBytes()
	if !biggest.EvictOldestDetail() {
		return 0, false
	}
	return before - biggest.EstimatedBytes(), true
}

func (s *Store) snapshotPlatforms() []*platformState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*platformState, 0, len(s.platforms))
	for _, p := range s.platforms {
		out = append(out, p)
	}
	return out
}

// Platforms lists the platforms that have hot state, sorted by name.
func (s *Store) Platforms() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.platforms))
	for name := range s.platforms {
		if name == "" {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Stats reports what each signal is holding, keyed "platform/signal".
func (s *Store) Stats() map[string]Stats {
	out := map[string]Stats{}
	s.mu.RLock()
	platforms := make(map[string]*platformState, len(s.platforms))
	for name, p := range s.platforms {
		platforms[name] = p
	}
	s.mu.RUnlock()
	for name, p := range platforms {
		p.mu.RLock()
		for sig, ser := range p.series {
			out[name+"/"+sig] = ser.Stats()
		}
		p.mu.RUnlock()
	}
	return out
}
