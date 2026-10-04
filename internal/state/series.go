package state

import (
	"slices"
	"sort"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/signal"
)

// MinuteOf returns the epoch minute a time falls in. Minute keys are integers
// everywhere in the server; a time zone is applied only when the browser
// formats them.
func MinuteOf(t time.Time) int64 { return t.Unix() / 60 }

// Series is the hot state of one signal of one platform: a ring of minutes,
// the newest `hot_detail_minutes` of them with full dimension breakdowns and
// the rest as totals only.
type Series struct {
	def *signal.Definition

	minutes map[int64]*minute
	// order holds the minute keys, ascending, so trimming is a slice cut.
	order []int64

	// interned shares one copy of each dimension value across every minute
	// that mentions it.
	interned map[string]string

	active []Pair

	// labels is dimension -> value -> the name another dimension gave it.
	labels map[string]map[string]string

	// detailKeys and labelCount keep the memory estimate cheap to read.
	detailKeys int
	labelCount int

	// counters that the metrics endpoint and the HM errors panel show.
	cappedTotal  int64
	demotedTotal int64
	evictedTotal int64
}

// NewSeries builds the hot state for one definition.
func NewSeries(def *signal.Definition) *Series {
	s := &Series{
		def:      def,
		minutes:  make(map[int64]*minute),
		interned: make(map[string]string),
	}
	s.SetActivePairs(nil)
	return s
}

// Definition is the signal this series belongs to.
func (s *Series) Definition() *signal.Definition { return s.def }

// SetActivePairs replaces the dimension pairs kept precomputed, always adding
// the ones the definition keeps. Pairs added here start filling from the next
// packet; older minutes answer from SQLite.
func (s *Series) SetActivePairs(pairs []Pair) {
	s.active = append(s.active[:0], pairs...)
	for _, kp := range s.def.KeepPairs {
		p := Pair{Main: kp[0], Sub: kp[1]}
		if !slices.Contains(s.active, p) {
			s.active = append(s.active, p)
		}
	}
}

// RestoredMinute is one minute read back from disk: its total and, when it is
// still inside the detail tier, its breakdowns.
type RestoredMinute struct {
	Minute int64
	Total  Agg
	Dims   map[string]map[string]Agg
	Pairs  map[Pair]map[string]map[string]Agg
}

// Restore puts a stored minute back as it was. A minute already in memory is
// left alone: live data is never replaced by an older copy.
func (s *Series) Restore(r RestoredMinute, now time.Time) {
	if _, ok := s.minutes[r.Minute]; ok {
		return
	}
	m := newMinute(r.Minute, r.Minute > MinuteOf(now)-int64(s.def.Retention.HotDetailMinutes))
	m.total = r.Total
	if m.detailed() {
		for dim, byValue := range r.Dims {
			into := make(map[string]*Agg, len(byValue))
			for value, agg := range byValue {
				a := agg
				into[s.intern(value)] = &a
			}
			m.keys += len(into)
			m.dims[dim] = into
		}
		for p, byMain := range r.Pairs {
			if m.pairs == nil {
				m.pairs = make(map[Pair]map[string]map[string]*Agg)
			}
			into := make(map[string]map[string]*Agg, len(byMain))
			for mainValue, bySub := range byMain {
				subs := make(map[string]*Agg, len(bySub))
				for subValue, agg := range bySub {
					a := agg
					subs[s.intern(subValue)] = &a
				}
				m.keys += len(subs)
				into[s.intern(mainValue)] = subs
			}
			m.pairs[p] = into
		}
	}
	s.minutes[r.Minute] = m
	s.detailKeys += m.keys
	s.insertOrder(r.Minute)
}

// ActivePairs lists the pairs currently maintained.
func (s *Series) ActivePairs() []Pair { return s.active }

func (s *Series) intern(v string) string {
	if have, ok := s.interned[v]; ok {
		return have
	}
	// The table is bounded by the same cap as a dimension map, so a flood of
	// unique values cannot grow it without limit.
	if len(s.interned) < s.def.MaxKeys*len(s.def.Dims)+len(s.def.Dims) {
		s.interned[v] = v
	}
	return v
}

// Observe folds one aggregate into the minute it belongs to. Dims may contain
// dimensions the signal does not declare; they are ignored.
func (s *Series) Observe(minuteKey int64, dims map[string]string, in Agg, now time.Time) {
	m, ok := s.minutes[minuteKey]
	if !ok {
		// A minute that is already outside the detail window (a late or
		// replayed packet) is created as totals-only.
		detail := minuteKey > MinuteOf(now)-int64(s.def.Retention.HotDetailMinutes)
		m = newMinute(minuteKey, detail)
		s.minutes[minuteKey] = m
		s.insertOrder(minuteKey)
	}
	before, keys := m.cappedCount(), m.keys
	m.observe(dims, s.def.Dims, s.active, s.def.MaxKeys, in, s.intern)
	s.cappedTotal += m.cappedCount() - before
	s.detailKeys += m.keys - keys
}

// MaxLabelsPerDim bounds the names kept for one dimension, in memory and on disk.
const MaxLabelsPerDim = 50_000

// Label is the name one value of a dimension goes by.
type Label struct {
	Dim, Value, Name string
}

// learnLabels remembers what each labelled value is called; by is
// dimension -> the dimension naming it.
func (s *Series) learnLabels(by map[string]string, dims map[string]string) {
	for dim, labelDim := range by {
		s.setLabel(dim, dims[dim], dims[labelDim])
	}
}

func (s *Series) setLabel(dim, value, name string) {
	if value == "" || name == "" || name == value {
		return
	}
	if s.labels == nil {
		s.labels = make(map[string]map[string]string)
	}
	known, ok := s.labels[dim]
	if !ok {
		known = make(map[string]string)
		s.labels[dim] = known
	}
	have, ok := known[value]
	if have == name || (!ok && len(known) >= MaxLabelsPerDim) {
		return
	}
	if !ok {
		s.labelCount++
	}
	known[value] = name
}

// Label is the name learned for a value, empty when none is known.
func (s *Series) Label(dim, value string) string { return s.labels[dim][value] }

func (m *minute) cappedCount() int64 {
	var n int64
	for _, v := range m.capped {
		n += v
	}
	return n
}

func (s *Series) insertOrder(key int64) {
	i := sort.Search(len(s.order), func(i int) bool { return s.order[i] >= key })
	s.order = append(s.order, 0)
	copy(s.order[i+1:], s.order[i:])
	s.order[i] = key
}

// Maintain applies the retention tiers: minutes older than the detail window
// lose their dimension maps, minutes older than the totals window are dropped
// altogether. It is cheap enough to call on every flush.
func (s *Series) Maintain(now time.Time) {
	nowMin := MinuteOf(now)
	detailFrom := nowMin - int64(s.def.Retention.HotDetailMinutes)
	totalsFrom := nowMin - int64(s.def.Retention.HotTotalsMinutes)

	cut := 0
	for _, key := range s.order {
		m := s.minutes[key]
		if key <= totalsFrom {
			s.detailKeys -= m.keys
			delete(s.minutes, key)
			s.evictedTotal++
			cut++
			continue
		}
		if key <= detailFrom && m.detailed() {
			s.detailKeys -= m.keys
			m.demote()
			s.demotedTotal++
		}
	}
	if cut > 0 {
		s.order = append(s.order[:0], s.order[cut:]...)
	}
}

// EvictOldestDetail drops the dimension maps of the oldest detailed minute,
// which is how the memory guard makes room without losing any totals.
func (s *Series) EvictOldestDetail() bool {
	for _, key := range s.order {
		if m := s.minutes[key]; m != nil && m.detailed() {
			s.detailKeys -= m.keys
			m.demote()
			s.demotedTotal++
			return true
		}
	}
	return false
}

// Rough per-entry costs, used only for the memory guard. They are estimates:
// the guard exists to keep the process inside a container limit, not to
// account for bytes exactly.
const (
	bytesPerDimensionKey = 100
	bytesPerMinute       = 200
	bytesPerInterned     = 48
)

// EstimatedBytes is roughly how much memory this series holds. Dimension
// values are interned, so their bytes are counted once rather than per minute.
func (s *Series) EstimatedBytes() int64 {
	return int64(s.detailKeys)*bytesPerDimensionKey +
		int64(len(s.order))*bytesPerMinute +
		int64(len(s.interned)+2*s.labelCount)*bytesPerInterned
}

// Stats reports what the series is holding, for metrics and the errors panel.
type Stats struct {
	Minutes        int   `json:"minutes"`
	DetailMinutes  int   `json:"detailMinutes"`
	DimensionKeys  int   `json:"dimensionKeys"`
	InternedValues int   `json:"internedValues"`
	Labels         int   `json:"labels"`
	Capped         int64 `json:"capped"`
	Demoted        int64 `json:"demoted"`
	Evicted        int64 `json:"evicted"`
	Oldest         int64 `json:"oldest"`
	Newest         int64 `json:"newest"`
	EstimatedBytes int64 `json:"estimatedBytes"`
}

// Stats summarises the series.
func (s *Series) Stats() Stats {
	st := Stats{
		Minutes:        len(s.order),
		DimensionKeys:  s.detailKeys,
		InternedValues: len(s.interned),
		Labels:         s.labelCount,
		Capped:         s.cappedTotal,
		Demoted:        s.demotedTotal,
		Evicted:        s.evictedTotal,
		EstimatedBytes: s.EstimatedBytes(),
	}
	if len(s.order) > 0 {
		st.Oldest = s.order[0]
		st.Newest = s.order[len(s.order)-1]
	}
	for _, m := range s.minutes {
		if m.detailed() {
			st.DetailMinutes++
		}
	}
	return st
}
