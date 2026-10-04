package state

import (
	"sort"
	"time"
)

// Point is one minute of a chart line.
type Point struct {
	Minute int64   `json:"minute"`
	Count  int64   `json:"count"`
	AvgMS  float64 `json:"avgMs"`
	MinMS  float64 `json:"minMs"`
	MaxMS  float64 `json:"maxMs"`
}

func pointOf(key int64, a Agg) Point {
	return Point{Minute: key, Count: a.Count, AvgMS: a.AvgMS(), MinMS: a.MinMS, MaxMS: a.MaxMS}
}

// Group is one value of the group-by dimension: its own line, its share of the
// window, and optionally the sub-dimension drawn inside it.
type Group struct {
	Value  string  `json:"value"`
	Label  string  `json:"label,omitempty"`
	Count  int64   `json:"count"`
	AvgMS  float64 `json:"avgMs"`
	Points []Point `json:"points"`
	Groups []Group `json:"groups,omitempty"`
}

// View is what one session sees of one signal: the main chart, the tiles, and
// the honest edges — which part of the window came from full detail and where
// a cardinality cap was hit.
type View struct {
	Platform string `json:"platform"`
	Signal   string `json:"signal"`
	From     int64  `json:"from"`
	To       int64  `json:"to"`

	Total  []Point `json:"total"`
	Group  string  `json:"group,omitempty"`
	Sub    string  `json:"sub,omitempty"`
	Groups []Group `json:"groups,omitempty"`

	// DetailFrom is the first minute for which dimension breakdowns are still
	// kept in memory. Anything before it has totals only, and a grouped view
	// of that part has to come from SQLite.
	DetailFrom int64 `json:"detailFrom"`
	// Capped counts values folded into the __other__ bucket, per dimension.
	Capped map[string]int64 `json:"capped,omitempty"`
	// Partial says the group breakdown covers only part of its window.
	Partial bool `json:"partial,omitempty"`
	// GroupsFrom is the first minute of the group tiles' window.
	GroupsFrom int64 `json:"groupsFrom,omitempty"`
	// Filtered says Total counts only the groups the filter keeps, which is
	// known only where the minutes still have their breakdown.
	Filtered bool `json:"filtered,omitempty"`
}

// View builds what a session should see. Minutes outside the hot window are
// simply absent; the browser asks /api/series for those.
func (s *Series) View(c Criteria, now time.Time) View {
	c = c.Normalise()
	to := MinuteOf(now)
	from := to - int64(c.HistoryMinutes) + 1
	return s.build(c, from, to, nil)
}

// MinuteView is the same shape for a single minute, which is what the hub
// broadcasts when a minute changes. The group membership is still decided
// over the whole window, so a tile does not appear and vanish between updates.
func (s *Series) MinuteView(c Criteria, minuteKey int64, now time.Time) View {
	return s.MinutesView(c, []int64{minuteKey}, now)
}

// MinutesView is MinuteView for several changed minutes at once: one view
// carrying the points of each, with the window ranked once for all of them.
func (s *Series) MinutesView(c Criteria, minuteKeys []int64, now time.Time) View {
	c = c.Normalise()
	to := MinuteOf(now)
	only := make(map[int64]bool, len(minuteKeys))
	for _, k := range minuteKeys {
		only[k] = true
		to = max(to, k)
	}
	from := to - int64(c.HistoryMinutes) + 1
	return s.build(c, from, to, only)
}

// skip reports whether a minute is left out of a view limited to only.
func skip(only map[int64]bool, m *minute) bool { return only != nil && !only[m.key] }

func (s *Series) build(c Criteria, from, to int64, only map[int64]bool) View {
	v := View{
		Platform: s.def.Platform,
		Signal:   s.def.Name,
		From:     from,
		To:       to,
		Group:    c.Group,
		Sub:      c.Sub,
		// Never nil: a nil slice marshals to null, and a client that is told
		// "points: null" where the shape promises a list has no good options.
		Total: []Point{},
	}

	window := s.window(from, to)
	v.Filtered = c.Filtered()
	kept := map[string]bool{}
	keeps := func(value string) bool {
		k, ok := kept[value]
		if !ok {
			k = c.Keeps(value, s.Label(c.Group, value))
			kept[value] = k
		}
		return k
	}
	for _, m := range window {
		if skip(only, m) {
			continue
		}
		if !v.Filtered {
			v.Total = append(v.Total, pointOf(m.key, m.total))
			continue
		}
		if m.dims == nil {
			continue
		}
		var sum Agg
		for value, agg := range m.dims[c.Group] {
			if keeps(value) {
				sum.Merge(*agg)
			}
		}
		v.Total = append(v.Total, pointOf(m.key, sum))
	}

	// Where detail ends is a property of the retention window, not of where
	// the data happens to start: an empty stretch of minutes is not a gap in
	// the breakdown.
	detailFrom := to - int64(s.def.Retention.HotDetailMinutes) + 1
	if detailFrom < from {
		detailFrom = from
	}
	v.DetailFrom = detailFrom
	groupsFrom := from
	if c.Group != "" {
		groupsFrom = to - int64(c.GroupMinutes) + 1
	}
	v.Partial = to-int64(s.def.Retention.HotDetailMinutes)+1 > groupsFrom

	capped := map[string]int64{}
	for _, m := range window {
		for dim, n := range m.capped {
			capped[dim] += n
		}
	}
	if len(capped) > 0 {
		v.Capped = capped
	}

	if c.Group == "" {
		return v
	}
	v.GroupsFrom = groupsFrom
	v.Groups = s.groups(c, s.window(groupsFrom, to), only)
	return v
}

// window returns the minutes of [from, to] in ascending order.
func (s *Series) window(from, to int64) []*minute {
	lo := sort.Search(len(s.order), func(i int) bool { return s.order[i] >= from })
	out := make([]*minute, 0, len(s.order)-lo)
	for _, key := range s.order[lo:] {
		if key > to {
			break
		}
		if m := s.minutes[key]; m != nil {
			out = append(out, m)
		}
	}
	return out
}

// ranked is a dimension value with its totals over the window, used to decide
// the top-N before any points are built.
type ranked struct {
	value string
	agg   Agg
}

func rank(totals map[string]*Agg, f filter, label func(string) string, sortBy string, top int) []ranked {
	out := make([]ranked, 0, len(totals))
	for value, agg := range totals {
		if !f.matches(value) && !f.matches(label(value)) {
			continue
		}
		out = append(out, ranked{value: value, agg: *agg})
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].agg.Value(sortBy), out[j].agg.Value(sortBy)
		if a != b {
			return a > b
		}
		return out[i].value < out[j].value
	})
	if top > 0 && len(out) > top {
		out = out[:top]
	}
	return out
}

func (s *Series) groups(c Criteria, window []*minute, only map[int64]bool) []Group {
	// Rank over the whole window, exactly as the old server did before
	// trimming to the top N.
	totals := make(map[string]*Agg)
	for _, m := range window {
		for value, agg := range m.dims[c.Group] {
			t, ok := totals[value]
			if !ok {
				t = &Agg{}
				totals[value] = t
			}
			t.Merge(*agg)
		}
	}
	if len(totals) == 0 {
		return nil
	}

	pair, wantPair := c.Pair()
	label := func(v string) string { return s.Label(c.Group, v) }
	top := rank(totals, newFilter(c.GroupFilter), label, c.SortBy, c.GroupTop)
	groups := make([]Group, 0, len(top))
	for _, r := range top {
		// An update carries points only for its own minutes, so a
		// group that saw nothing in it has an empty list — not a missing one.
		g := Group{Value: r.value, Label: label(r.value), Count: r.agg.Count, AvgMS: r.agg.AvgMS(), Points: []Point{}}
		for _, m := range window {
			if skip(only, m) {
				continue
			}
			if agg, ok := m.dims[c.Group][r.value]; ok {
				g.Points = append(g.Points, pointOf(m.key, *agg))
			}
		}
		if wantPair {
			g.Groups = s.subGroups(c, pair, r.value, window, only)
		}
		groups = append(groups, g)
	}
	return groups
}

func (s *Series) subGroups(c Criteria, pair Pair, mainValue string, window []*minute, only map[int64]bool) []Group {
	totals := make(map[string]*Agg)
	for _, m := range window {
		for value, agg := range m.pairs[pair][mainValue] {
			t, ok := totals[value]
			if !ok {
				t = &Agg{}
				totals[value] = t
			}
			t.Merge(*agg)
		}
	}
	if len(totals) == 0 {
		return nil
	}
	label := func(v string) string { return s.Label(pair.Sub, v) }
	top := rank(totals, newFilter(c.SubFilter), label, c.SortBy, c.SubTop)
	out := make([]Group, 0, len(top))
	for _, r := range top {
		g := Group{Value: r.value, Label: label(r.value), Count: r.agg.Count, AvgMS: r.agg.AvgMS(), Points: []Point{}}
		for _, m := range window {
			if skip(only, m) {
				continue
			}
			if agg, ok := m.pairs[pair][mainValue][r.value]; ok {
				g.Points = append(g.Points, pointOf(m.key, *agg))
			}
		}
		out = append(out, g)
	}
	return out
}
