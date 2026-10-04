package state

// minute is one minute of one signal. It always carries the totals; the
// per-dimension maps exist only while the minute is inside the detail tier,
// after which they are dropped and the totals stay.
type minute struct {
	key   int64
	total Agg

	// dims is dimension -> value -> aggregate. Nil once demoted to totals.
	dims map[string]map[string]*Agg

	// pairs holds main -> sub breakdowns, but only for the pairs some session
	// is currently looking at.
	pairs map[Pair]map[string]map[string]*Agg

	// capped counts the distinct values per dimension that were folded into
	// OtherBucket because the cardinality cap was reached.
	capped map[string]int64

	// keys counts the aggregates in dims and pairs, for the memory estimate.
	keys int
}

func newMinute(key int64, detail bool) *minute {
	m := &minute{key: key}
	if detail {
		m.dims = make(map[string]map[string]*Agg)
	}
	return m
}

// demote drops the per-dimension detail, keeping the totals. SQLite still has
// the full breakdown, so nothing is lost, only the fast path.
func (m *minute) demote() {
	m.dims = nil
	m.pairs = nil
	m.keys = 0
}

// detailed reports whether this minute still carries dimension breakdowns.
func (m *minute) detailed() bool { return m.dims != nil }

// observe folds one aggregate into the minute.
func (m *minute) observe(dims map[string]string, declared []string, active []Pair, maxKeys int, in Agg, intern func(string) string) {
	m.total.Merge(in)
	if m.dims == nil {
		return
	}
	for _, name := range declared {
		value, ok := dims[name]
		if !ok || value == "" {
			continue
		}
		byValue, ok := m.dims[name]
		if !ok {
			byValue = make(map[string]*Agg)
			m.dims[name] = byValue
		}
		_, known := byValue[value]
		key := m.keyFor(name, value, known, len(byValue), maxKeys, intern)
		agg, ok := byValue[key]
		if !ok {
			agg = &Agg{}
			byValue[key] = agg
			m.keys++
		}
		agg.Merge(in)
	}
	for _, p := range active {
		mainValue, ok := dims[p.Main]
		if !ok || mainValue == "" {
			continue
		}
		subValue, ok := dims[p.Sub]
		if !ok || subValue == "" {
			continue
		}
		if m.pairs == nil {
			m.pairs = make(map[Pair]map[string]map[string]*Agg)
		}
		byMain, ok := m.pairs[p]
		if !ok {
			byMain = make(map[string]map[string]*Agg)
			m.pairs[p] = byMain
		}
		// The pair reuses the caps of its own dimensions, so a pair can never
		// hold more keys than the single-dimension maps allow.
		_, knownMain := byMain[mainValue]
		mainKey := m.keyFor(p.Main, mainValue, knownMain, len(byMain), maxKeys, intern)
		bySub, ok := byMain[mainKey]
		if !ok {
			bySub = make(map[string]*Agg)
			byMain[mainKey] = bySub
		}
		_, knownSub := bySub[subValue]
		subKey := m.keyFor(p.Sub, subValue, knownSub, len(bySub), maxKeys, intern)
		agg, ok := bySub[subKey]
		if !ok {
			agg = &Agg{}
			bySub[subKey] = agg
			m.keys++
		}
		agg.Merge(in)
	}
}

// keyFor returns the key a value is stored under: the value itself while the
// dimension has room, OtherBucket once the cardinality cap is reached. A value
// already present always keeps its own key.
func (m *minute) keyFor(dim, value string, known bool, size, maxKeys int, intern func(string) string) string {
	if known {
		return value
	}
	if maxKeys > 0 && size >= maxKeys {
		m.countCapped(dim)
		return OtherBucket
	}
	return intern(value)
}

func (m *minute) countCapped(dim string) {
	if m.capped == nil {
		m.capped = make(map[string]int64)
	}
	m.capped[dim]++
}
