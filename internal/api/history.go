package api

import (
	"context"
	"sort"

	"github.com/Sznapsollo/health-monitor-nj/internal/state"
	"github.com/Sznapsollo/health-monitor-nj/internal/store"
)

// withHistory fills in the part of a window the hot tiers cannot answer.
// Memory keeps two hours of full detail and two days of totals; anything
// older or deeper comes from SQLite on demand, which is what makes "yesterday
// afternoon, grouped by url" a query rather than a memory budget.
func (d Deps) withHistory(ctx context.Context, platform string, c state.Criteria, hot state.View) state.View {
	if d.DB == nil {
		return hot
	}
	if hot.Filtered {
		return d.filteredHistory(ctx, platform, c, hot)
	}

	// The totals line first: the hot tier holds 48 h by default, so this only
	// does anything for a window reaching past that.
	if from := hot.From; len(hot.Total) == 0 || hot.Total[0].Minute > from {
		until := hot.To
		if len(hot.Total) > 0 {
			until = hot.Total[0].Minute - 1
		}
		if rows, err := d.DB.Series(ctx, store.Query{
			Platform: platform, Signal: c.Signal, From: from, To: until,
		}); err == nil && len(rows) > 0 {
			hot.Total = append(pointsOf(rows), hot.Total...)
		} else if err != nil {
			d.Log.Debug("history totals unavailable", "signal", c.Signal, "error", err)
		}
	}

	if c.Group == "" || !hot.Partial {
		return hot
	}

	rows, err := d.DB.Series(ctx, store.Query{
		Platform: platform, Signal: c.Signal,
		From: hot.From, To: hot.DetailFrom - 1, Group: c.Group,
	})
	if err != nil {
		d.Log.Debug("history breakdown unavailable", "signal", c.Signal, "error", err)
		return hot
	}
	if len(rows) == 0 {
		return hot
	}

	hot.Groups = mergeHistory(hot.Groups, d.kept(platform, c, rows), c)
	for i, g := range hot.Groups {
		if g.Label == "" {
			hot.Groups[i].Label = d.Store.Label(platform, c.Signal, c.Group, g.Value)
		}
	}
	// The breakdown now covers the whole window, so it is no longer partial —
	// though the older part has no sub-series, which only memory keeps.
	hot.Partial = false
	hot.DetailFrom = hot.From
	return hot
}

// mergeHistory ranks over the whole window, not just the part still in
// memory, so the top N does not change shape at the tier boundary.
func mergeHistory(hotGroups []state.Group, rows []store.Row, c state.Criteria) []state.Group {
	type acc struct {
		agg    state.Agg
		points []state.Point
	}
	byValue := map[string]*acc{}

	for _, r := range rows {
		a, ok := byValue[r.Value]
		if !ok {
			a = &acc{}
			byValue[r.Value] = a
		}
		a.agg.Merge(r.Agg)
		a.points = append(a.points, state.Point{
			Minute: r.Minute, Count: r.Agg.Count, AvgMS: r.Agg.AvgMS(),
			MinMS: r.Agg.MinMS, MaxMS: r.Agg.MaxMS,
		})
	}

	hotByValue := map[string]state.Group{}
	for _, g := range hotGroups {
		hotByValue[g.Value] = g
		a, ok := byValue[g.Value]
		if !ok {
			a = &acc{}
			byValue[g.Value] = a
		}
		a.agg.Add(g.Count, 0, false)
		if g.AvgMS > 0 {
			// The hot half already averaged; carry it as one measurement so
			// the ranking by latency is not thrown off by the disk half.
			a.agg.AddSummed(0, g.AvgMS, 1, g.AvgMS, g.AvgMS, true)
		}
	}

	ranked := make([]string, 0, len(byValue))
	for value := range byValue {
		ranked = append(ranked, value)
	}
	sort.Slice(ranked, func(i, j int) bool {
		a, b := byValue[ranked[i]].agg, byValue[ranked[j]].agg
		av, bv := a.Value(c.SortBy), b.Value(c.SortBy)
		if av != bv {
			return av > bv
		}
		return ranked[i] < ranked[j]
	})
	if c.GroupTop > 0 && len(ranked) > c.GroupTop {
		ranked = ranked[:c.GroupTop]
	}

	out := make([]state.Group, 0, len(ranked))
	for _, value := range ranked {
		a := byValue[value]
		g := hotByValue[value]
		sort.Slice(a.points, func(i, j int) bool { return a.points[i].Minute < a.points[j].Minute })
		out = append(out, state.Group{
			Value:  value,
			Label:  g.Label,
			Count:  a.agg.Count,
			AvgMS:  a.agg.AvgMS(),
			Points: append(a.points, g.Points...),
			// Sub-series exist only for the part still in memory.
			Groups: g.Groups,
		})
	}
	return out
}

func pointsOf(rows []store.Row) []state.Point {
	out := make([]state.Point, 0, len(rows))
	for _, r := range rows {
		out = append(out, state.Point{
			Minute: r.Minute, Count: r.Agg.Count, AvgMS: r.Agg.AvgMS(),
			MinMS: r.Agg.MinMS, MaxMS: r.Agg.MaxMS,
		})
	}
	return out
}

// kept drops the rows of values the group filter does not keep.
func (d Deps) kept(platform string, c state.Criteria, rows []store.Row) []store.Row {
	if !c.Filtered() {
		return rows
	}
	out := rows[:0:0]
	for _, r := range rows {
		if c.Keeps(r.Value, d.Store.Label(platform, c.Signal, c.Group, r.Value)) {
			out = append(out, r)
		}
	}
	return out
}

// filteredHistory is withHistory for a filtered view: the older part of the
// main chart is summed from the kept values' rows, since the stored totals
// count everything.
func (d Deps) filteredHistory(ctx context.Context, platform string, c state.Criteria, hot state.View) state.View {
	until := hot.To
	if len(hot.Total) > 0 {
		until = hot.Total[0].Minute - 1
	}
	if until < hot.From {
		return hot
	}
	rows, err := d.DB.Series(ctx, store.Query{
		Platform: platform, Signal: c.Signal, From: hot.From, To: until, Group: c.Group,
	})
	if err != nil {
		d.Log.Debug("filtered history unavailable", "signal", c.Signal, "error", err)
		return hot
	}
	rows = d.kept(platform, c, rows)
	byMinute := map[int64]*state.Agg{}
	var minutes []int64
	for _, r := range rows {
		a, ok := byMinute[r.Minute]
		if !ok {
			a = &state.Agg{}
			byMinute[r.Minute] = a
			minutes = append(minutes, r.Minute)
		}
		a.Merge(r.Agg)
	}
	sort.Slice(minutes, func(i, j int) bool { return minutes[i] < minutes[j] })
	older := make([]state.Point, 0, len(minutes))
	for _, m := range minutes {
		a := byMinute[m]
		older = append(older, state.Point{Minute: m, Count: a.Count, AvgMS: a.AvgMS(), MinMS: a.MinMS, MaxMS: a.MaxMS})
	}
	hot.Total = append(older, hot.Total...)

	if hot.Partial && len(rows) > 0 {
		var breakdown []store.Row
		for _, r := range rows {
			if r.Minute < hot.DetailFrom {
				breakdown = append(breakdown, r)
			}
		}
		hot.Groups = mergeHistory(hot.Groups, breakdown, c)
		for i, g := range hot.Groups {
			if g.Label == "" {
				hot.Groups[i].Label = d.Store.Label(platform, c.Signal, c.Group, g.Value)
			}
		}
		hot.Partial = false
		hot.DetailFrom = hot.From
	}
	return hot
}
