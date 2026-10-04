package state

import (
	"strings"
)

// Defaults for a viewer who has not chosen anything yet. They match the old
// UI: 50 values per chart, one hour of history.
const (
	DefaultHistoryMinutes = 60
	DefaultGroupMinutes   = 120
	DefaultTop            = 50
	MaxHistoryMinutes     = 1600
	MinHistoryMinutes     = 5
)

// Criteria is one viewer's question about one signal: how far back, grouped by
// what, how many values, filtered how. It is the "Filtry" panel of the old UI
// and decides what the server sends that session.
type Criteria struct {
	// ID tells two views of one signal apart; empty means the signal name.
	ID     string `json:"id,omitempty"`
	Signal string `json:"signal"`

	// HistoryMinutes is how many minutes back the viewer wants.
	HistoryMinutes int `json:"historyMinutes,omitempty"`
	// GroupMinutes is the group tiles' own window, 120 when not given.
	GroupMinutes int `json:"groupMinutes,omitempty"`

	// Group is the dimension the per-value tiles are built from; empty means
	// the main chart only.
	Group       string `json:"group,omitempty"`
	GroupTop    int    `json:"groupTop,omitempty"`
	GroupFilter string `json:"groupFilter,omitempty"`

	// Sub is the dimension drawn as series inside each tile.
	Sub       string `json:"sub,omitempty"`
	SubTop    int    `json:"subTop,omitempty"`
	SubFilter string `json:"subFilter,omitempty"`
	Stacked   *bool  `json:"stacked,omitempty"`
	// Main hides the main chart when false; only with a Group.
	Main *bool `json:"main,omitempty"`
	// Together is the browser's: every group in one stacked chart.
	Together bool `json:"together,omitempty"`

	// SortBy ranks the values before the top-N cut: "count" or "avgMs".
	SortBy string `json:"sortBy,omitempty"`
}

// Normalise fills in defaults and clamps what the browser may have sent.
func (c Criteria) Normalise() Criteria {
	if c.ID == "" {
		c.ID = c.Signal
	}
	if c.HistoryMinutes <= 0 {
		c.HistoryMinutes = DefaultHistoryMinutes
	}
	if c.HistoryMinutes < MinHistoryMinutes {
		c.HistoryMinutes = MinHistoryMinutes
	}
	if c.HistoryMinutes > MaxHistoryMinutes {
		c.HistoryMinutes = MaxHistoryMinutes
	}
	if c.GroupMinutes <= 0 {
		c.GroupMinutes = DefaultGroupMinutes
	}
	c.GroupMinutes = max(MinHistoryMinutes, min(c.GroupMinutes, MaxHistoryMinutes))
	if c.GroupTop <= 0 {
		c.GroupTop = DefaultTop
	}
	if c.SubTop <= 0 {
		c.SubTop = DefaultTop
	}
	switch c.SortBy {
	case "count", "avgMs":
	default:
		c.SortBy = "count"
	}
	return c
}

// Pair is the dimension pair a session needs precomputed. Cubes keep only the
// pairs some session is actually looking at.
type Pair struct {
	Main string
	Sub  string
}

// Pair reports the dimension pair this criteria needs, if any.
func (c Criteria) Pair() (Pair, bool) {
	if c.Group == "" || c.Sub == "" || c.Group == c.Sub {
		return Pair{}, false
	}
	return Pair{Main: c.Group, Sub: c.Sub}, true
}

// filter is a comma-separated list of case-insensitive substrings, exactly as
// the old UI's search boxes worked: a value passes if it contains any of them.
type filter struct {
	terms []string
}

// Filtered says the main chart follows the group filter: a group and a filter
// are both set.
func (c Criteria) Filtered() bool { return c.Group != "" && newFilter(c.GroupFilter).any() }

// Keeps says a group value (or its name) passes the group filter.
func (c Criteria) Keeps(value, label string) bool {
	f := newFilter(c.GroupFilter)
	return f.matches(value) || (label != "" && f.matches(label))
}

func (f filter) any() bool { return len(f.terms) > 0 }

func newFilter(s string) filter {
	var f filter
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(strings.ToLower(part))
		if part != "" {
			f.terms = append(f.terms, part)
		}
	}
	return f
}

func (f filter) matches(value string) bool {
	if len(f.terms) == 0 {
		return true
	}
	lower := strings.ToLower(value)
	for _, t := range f.terms {
		if strings.Contains(lower, t) {
			return true
		}
	}
	return false
}
