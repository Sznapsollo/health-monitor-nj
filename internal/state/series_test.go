package state_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/signal"
	"github.com/Sznapsollo/health-monitor-nj/internal/state"
)

var base = time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)

func testDef(dims ...string) *signal.Definition {
	d := &signal.Definition{
		Platform: "test",
		Name:     "requests",
		Kind:     signal.KindTimeseries,
		Dims:     dims,
		Retention: signal.Retention{
			HotDetailMinutes: 5,
			HotTotalsMinutes: 20,
			DurableDays:      30,
		},
		MaxKeys: 100,
	}
	return d
}

// observe folds one request of ms milliseconds into the minute base+offset.
func observe(s *state.Series, offset int, ms float64, dims map[string]string) {
	now := base.Add(time.Duration(offset) * time.Minute)
	var a state.Agg
	a.Add(1, ms, true)
	s.Observe(state.MinuteOf(now), dims, a, now)
}

func TestAggMerge(t *testing.T) {
	var a state.Agg
	a.Add(1, 100, true)
	a.Add(1, 300, true)
	if a.Count != 2 || a.AvgMS() != 200 || a.MinMS != 100 || a.MaxMS != 300 {
		t.Fatalf("agg = %+v, avg = %v", a, a.AvgMS())
	}

	// One report of three items that took 50 ms: three items, one measurement.
	var b state.Agg
	b.Add(3, 50, true)
	a.Merge(b)
	if a.Count != 5 || a.MinMS != 50 || a.MaxMS != 300 {
		t.Fatalf("merged = %+v", a)
	}
	if got, want := a.AvgMS(), (100+300+50)/3.0; got != want {
		t.Fatalf("avg = %v, want %v — durations average per measurement, not per item", got, want)
	}

	// A sender that pre-aggregated ten requests into one packet.
	var c state.Agg
	c.AddSummed(10, 1000, 10, 20, 400, true)
	if c.Count != 10 || c.Samples != 10 || c.AvgMS() != 100 || c.MinMS != 20 || c.MaxMS != 400 {
		t.Fatalf("pre-aggregated = %+v, avg = %v", c, c.AvgMS())
	}

	// A signal without durations only counts.
	var d state.Agg
	d.Add(2, 0, false)
	if d.Count != 2 || d.AvgMS() != 0 || d.Samples != 0 {
		t.Fatalf("countless agg = %+v", d)
	}
}

func TestViewTotalsPerMinute(t *testing.T) {
	s := state.NewSeries(testDef("url"))
	observe(s, 0, 100, map[string]string{"url": "/a"})
	observe(s, 0, 300, map[string]string{"url": "/b"})
	observe(s, 1, 200, map[string]string{"url": "/a"})

	v := s.View(state.Criteria{HistoryMinutes: 5}, base.Add(time.Minute))
	if len(v.Total) != 2 {
		t.Fatalf("total points = %d, want one per minute: %+v", len(v.Total), v.Total)
	}
	if v.Total[0].Count != 2 {
		t.Errorf("first minute count = %d, want 2", v.Total[0].Count)
	}
	if v.Total[0].AvgMS != 200 {
		t.Errorf("first minute avg = %v, want 200", v.Total[0].AvgMS)
	}
	if v.Total[1].Count != 1 || v.Total[1].AvgMS != 200 {
		t.Errorf("second minute = %+v", v.Total[1])
	}
}

func TestViewGroupsAreRankedAndTrimmed(t *testing.T) {
	s := state.NewSeries(testDef("url", "user"))
	for i := 0; i < 5; i++ {
		observe(s, 0, 10, map[string]string{"url": "/busy", "user": "alice"})
	}
	observe(s, 0, 1000, map[string]string{"url": "/slow", "user": "bob"})
	observe(s, 0, 10, map[string]string{"url": "/quiet", "user": "carol"})

	// By count, the busy URL wins.
	v := s.View(state.Criteria{HistoryMinutes: 5, Group: "url", GroupTop: 2}, base)
	if len(v.Groups) != 2 {
		t.Fatalf("groups = %d, want the top 2: %+v", len(v.Groups), v.Groups)
	}
	if v.Groups[0].Value != "/busy" {
		t.Errorf("first group = %q, want /busy", v.Groups[0].Value)
	}

	// By average latency, the slow one does.
	v = s.View(state.Criteria{HistoryMinutes: 5, Group: "url", GroupTop: 1, SortBy: "avgMs"}, base)
	if len(v.Groups) != 1 || v.Groups[0].Value != "/slow" {
		t.Fatalf("groups = %+v, want /slow first", v.Groups)
	}

	// The substring filter is case-insensitive and comma-separated.
	v = s.View(state.Criteria{HistoryMinutes: 5, Group: "url", GroupFilter: "QUIET, slow"}, base)
	if len(v.Groups) != 2 {
		t.Fatalf("filtered groups = %+v, want /slow and /quiet", v.Groups)
	}
	for _, g := range v.Groups {
		if g.Value == "/busy" {
			t.Errorf("the filter let %q through", g.Value)
		}
	}
}

func TestSubGroupsNeedAnActivePair(t *testing.T) {
	s := state.NewSeries(testDef("url", "user"))
	c := state.Criteria{HistoryMinutes: 5, Group: "url", Sub: "user"}

	observe(s, 0, 10, map[string]string{"url": "/a", "user": "alice"})
	// Without the pair being active, the tile has no series inside it.
	if v := s.View(c, base); len(v.Groups) != 1 || len(v.Groups[0].Groups) != 0 {
		t.Fatalf("sub-groups appeared without an active pair: %+v", v.Groups)
	}

	pair, ok := c.Pair()
	if !ok {
		t.Fatal("criteria with a group and a sub should want a pair")
	}
	s.SetActivePairs([]state.Pair{pair})
	observe(s, 0, 20, map[string]string{"url": "/a", "user": "alice"})
	observe(s, 0, 30, map[string]string{"url": "/a", "user": "bob"})

	v := s.View(c, base)
	if len(v.Groups) != 1 {
		t.Fatalf("groups = %+v", v.Groups)
	}
	subs := v.Groups[0].Groups
	if len(subs) != 2 {
		t.Fatalf("sub-groups = %+v, want alice and bob", subs)
	}
	if subs[0].Value != "alice" || subs[0].Count != 1 {
		t.Errorf("first sub-group = %+v, want alice counted once from after the pair went active", subs[0])
	}
}

func TestRetentionTiers(t *testing.T) {
	def := testDef("url")
	s := state.NewSeries(def)
	for i := 0; i < 12; i++ {
		observe(s, i, 10, map[string]string{"url": "/a"})
	}
	now := base.Add(11 * time.Minute)
	s.Maintain(now)

	st := s.Stats()
	if st.Minutes != 12 {
		t.Fatalf("minutes = %d, want all 12 still inside the totals window", st.Minutes)
	}
	// Only the last hot_detail_minutes keep their dimension maps.
	if st.DetailMinutes != def.Retention.HotDetailMinutes {
		t.Errorf("detailed minutes = %d, want %d", st.DetailMinutes, def.Retention.HotDetailMinutes)
	}

	// Totals survive demotion, group breakdowns do not.
	v := s.View(state.Criteria{HistoryMinutes: 20, Group: "url"}, now)
	if len(v.Total) != 12 {
		t.Errorf("total points = %d, want every minute", len(v.Total))
	}
	if !v.Partial {
		t.Error("the view should say the group breakdown covers only part of the window")
	}
	if got := len(v.Groups[0].Points); got != def.Retention.HotDetailMinutes {
		t.Errorf("group points = %d, want only the detailed minutes", got)
	}

	// Past the totals window the minute goes altogether.
	s.Maintain(base.Add(40 * time.Minute))
	if st := s.Stats(); st.Minutes != 0 {
		t.Errorf("minutes = %d, want everything evicted", st.Minutes)
	}
}

func TestCardinalityCap(t *testing.T) {
	def := testDef("url")
	def.MaxKeys = 3
	s := state.NewSeries(def)
	for i := 0; i < 10; i++ {
		observe(s, 0, 10, map[string]string{"url": fmt.Sprintf("/u%d", i)})
	}

	v := s.View(state.Criteria{HistoryMinutes: 5, Group: "url", GroupTop: 100}, base)
	if len(v.Groups) != 4 {
		t.Fatalf("groups = %d, want 3 values plus the other bucket: %+v", len(v.Groups), v.Groups)
	}
	var other *state.Group
	for i := range v.Groups {
		if v.Groups[i].Value == state.OtherBucket {
			other = &v.Groups[i]
		}
	}
	if other == nil {
		t.Fatal("no __other__ bucket")
	}
	if other.Count != 7 {
		t.Errorf("__other__ count = %d, want the 7 values that did not fit", other.Count)
	}
	if v.Capped["url"] != 7 {
		t.Errorf("capped counter = %d, want 7", v.Capped["url"])
	}

	// Totals stay correct even when values are folded away.
	if v.Total[0].Count != 10 {
		t.Errorf("total = %d, want all 10 observations", v.Total[0].Count)
	}
}

func TestMinuteViewCarriesOneMinuteWithStableGroups(t *testing.T) {
	s := state.NewSeries(testDef("url"))
	observe(s, 0, 10, map[string]string{"url": "/a"})
	observe(s, 0, 10, map[string]string{"url": "/a"})
	observe(s, 1, 10, map[string]string{"url": "/a"})
	observe(s, 1, 10, map[string]string{"url": "/b"})

	now := base.Add(time.Minute)
	v := s.MinuteView(state.Criteria{HistoryMinutes: 5, Group: "url"}, state.MinuteOf(now), now)
	if len(v.Total) != 1 {
		t.Fatalf("total points = %d, want just the one minute", len(v.Total))
	}
	if v.Total[0].Count != 2 {
		t.Errorf("minute count = %d, want 2", v.Total[0].Count)
	}
	// Both values rank over the window, so both tiles get their update.
	if len(v.Groups) != 2 {
		t.Fatalf("groups = %+v", v.Groups)
	}
	for _, g := range v.Groups {
		if len(g.Points) != 1 {
			t.Errorf("group %q has %d points, want one", g.Value, len(g.Points))
		}
	}
}

func TestLateMinuteDoesNotGetDetail(t *testing.T) {
	def := testDef("url")
	s := state.NewSeries(def)
	now := base.Add(30 * time.Minute)
	// A packet whose minute is already outside the detail window: it still
	// counts in the totals, but no dimension map is built for it.
	var a state.Agg
	a.Add(1, 10, true)
	s.Observe(state.MinuteOf(base), map[string]string{"url": "/late"}, a, now)

	v := s.View(state.Criteria{HistoryMinutes: 60, Group: "url"}, now)
	if len(v.Total) != 1 || v.Total[0].Count != 1 {
		t.Fatalf("totals = %+v, want the late packet counted", v.Total)
	}
	if len(v.Groups) != 0 {
		t.Errorf("groups = %+v, want none for a minute outside the detail window", v.Groups)
	}
}

func TestEvictOldestDetail(t *testing.T) {
	s := state.NewSeries(testDef("url"))
	for i := 0; i < 3; i++ {
		observe(s, i, 10, map[string]string{"url": "/a"})
	}
	if !s.EvictOldestDetail() {
		t.Fatal("nothing was evicted")
	}
	st := s.Stats()
	if st.DetailMinutes != 2 {
		t.Errorf("detailed minutes = %d, want 2 after evicting one", st.DetailMinutes)
	}
	if st.Minutes != 3 {
		t.Errorf("minutes = %d, want the totals kept", st.Minutes)
	}
}

func TestPartialFollowsRetentionNotEmptyMinutes(t *testing.T) {
	def := testDef("url")
	s := state.NewSeries(def)
	observe(s, 0, 10, map[string]string{"url": "/a"})

	// A window shorter than the detail retention is complete, even though
	// most of its minutes hold nothing at all.
	v := s.View(state.Criteria{HistoryMinutes: def.Retention.HotDetailMinutes}, base)
	if v.Partial {
		t.Errorf("partial = true for a window inside the detail tier (detailFrom %d, from %d)",
			v.DetailFrom, v.From)
	}
	if v.DetailFrom != v.From {
		t.Errorf("detailFrom = %d, want the start of the window (%d)", v.DetailFrom, v.From)
	}

	// A window longer than the detail retention is honestly partial.
	v = s.View(state.Criteria{HistoryMinutes: def.Retention.HotDetailMinutes + 10}, base)
	if !v.Partial {
		t.Error("partial = false for a window reaching past the detail tier")
	}
	if v.DetailFrom <= v.From {
		t.Errorf("detailFrom = %d, want it inside the window starting at %d", v.DetailFrom, v.From)
	}
}

func TestEstimatedBytesGrowsWithWhatIsHeld(t *testing.T) {
	s := state.NewSeries(testDef("url"))
	if s.EstimatedBytes() != 0 {
		t.Fatalf("an empty series estimates %d bytes", s.EstimatedBytes())
	}

	observe(s, 0, 10, map[string]string{"url": "/a"})
	small := s.EstimatedBytes()
	if small <= 0 {
		t.Fatalf("estimate = %d after one observation", small)
	}

	for i := 1; i < 5; i++ {
		observe(s, i, 10, map[string]string{"url": fmt.Sprintf("/u%d", i)})
	}
	if big := s.EstimatedBytes(); big <= small {
		t.Errorf("estimate = %d, want more than %d after four more minutes", big, small)
	}

	// Demoting a minute gives its dimension keys back.
	before := s.EstimatedBytes()
	s.EvictOldestDetail()
	if after := s.EstimatedBytes(); after >= before {
		t.Errorf("estimate = %d after eviction, want less than %d", after, before)
	}
}

// A nil slice marshals to JSON null. The browser is promised a list, and a
// single-minute update is exactly where a group can legitimately have nothing
// to say — so these must be empty, never absent.
func TestViewsNeverCarryNilLists(t *testing.T) {
	s := state.NewSeries(testDef("url"))
	observe(s, 0, 10, map[string]string{"url": "/a"})
	observe(s, 1, 10, map[string]string{"url": "/b"})
	now := base.Add(time.Minute)

	c := state.Criteria{HistoryMinutes: 10, Group: "url"}

	v := s.View(c, now)
	if v.Total == nil {
		t.Error("total is nil in a full view")
	}
	for _, g := range v.Groups {
		if g.Points == nil {
			t.Errorf("group %q has nil points in a full view", g.Value)
		}
	}

	// The update for the second minute: /a saw nothing in it.
	update := s.MinuteView(c, state.MinuteOf(now), now)
	if update.Total == nil {
		t.Error("total is nil in a single-minute update")
	}
	var quiet *state.Group
	for i := range update.Groups {
		if update.Groups[i].Value == "/a" {
			quiet = &update.Groups[i]
		}
	}
	if quiet == nil {
		t.Fatalf("groups = %+v, want /a ranked over the window", update.Groups)
	}
	if quiet.Points == nil {
		t.Error("a group with nothing in the changed minute has nil points, which becomes null on the wire")
	}
	if len(quiet.Points) != 0 {
		t.Errorf("points = %+v, want none for a minute it did not appear in", quiet.Points)
	}

	// And the whole thing survives a round trip as real JSON.
	encoded, err := json.Marshal(update)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(`"points":null`)) || bytes.Contains(encoded, []byte(`"total":null`)) {
		t.Fatalf("the wire form contains a null list: %s", encoded)
	}
}

func TestGroupTilesHaveTheirOwnWindow(t *testing.T) {
	def := testDef("url")
	def.Retention.HotDetailMinutes = 20
	s := state.NewSeries(def)
	for offset := 0; offset < 10; offset++ {
		observe(s, offset, 10, map[string]string{"url": "/a"})
	}
	now := base.Add(9 * time.Minute)

	v := s.View(state.Criteria{HistoryMinutes: 10, GroupMinutes: 6, Group: "url"}, now)
	if len(v.Total) != 10 {
		t.Errorf("main chart has %d minutes, want 10", len(v.Total))
	}
	if len(v.Groups) != 1 || len(v.Groups[0].Points) != 6 || v.Groups[0].Count != 6 {
		t.Fatalf("groups = %+v, want one tile over the last 6 minutes", v.Groups)
	}
	if v.GroupsFrom != state.MinuteOf(now)-5 || v.Partial {
		t.Errorf("groupsFrom = %d, partial = %v", v.GroupsFrom, v.Partial)
	}

	unset := s.View(state.Criteria{HistoryMinutes: 10, Group: "url"}, now)
	if unset.GroupsFrom != state.MinuteOf(now)-state.DefaultGroupMinutes+1 || len(unset.Groups[0].Points) != 10 {
		t.Errorf("without groupMinutes the tiles span %d minutes back: %+v",
			state.MinuteOf(now)-unset.GroupsFrom+1, unset.Groups[0])
	}

	longer := s.View(state.Criteria{HistoryMinutes: 5, GroupMinutes: 30, Group: "url"}, now)
	if len(longer.Total) != 5 || len(longer.Groups[0].Points) != 10 || !longer.Partial {
		t.Errorf("tiles longer than detail: total %d, tile %d, partial %v",
			len(longer.Total), len(longer.Groups[0].Points), longer.Partial)
	}
}

func TestSeveralMinutesInOneViewMatchOneViewEach(t *testing.T) {
	s := state.NewSeries(testDef("url", "user"))
	s.SetActivePairs([]state.Pair{{Main: "url", Sub: "user"}})
	now := base.Add(10 * time.Minute)
	for m := 0; m < 5; m++ {
		at := now.Add(-time.Duration(m) * time.Minute)
		for i := 0; i <= m; i++ {
			var a state.Agg
			a.Add(1, float64(10*i), true)
			s.Observe(state.MinuteOf(at), map[string]string{"url": "/u" + string(rune('a'+i)), "user": "anna"}, a, now)
		}
	}
	c := state.Criteria{Signal: "requests", Group: "url", Sub: "user"}
	a, b := state.MinuteOf(now)-3, state.MinuteOf(now)-1

	both := s.MinutesView(c, []int64{a, b}, now)
	first, second := s.MinuteView(c, a, now), s.MinuteView(c, b, now)

	if len(both.Total) != 2 || both.Total[0] != first.Total[0] || both.Total[1] != second.Total[0] {
		t.Fatalf("totals = %+v, want %+v then %+v", both.Total, first.Total, second.Total)
	}
	if len(both.Groups) != len(first.Groups) {
		t.Fatalf("groups = %d, want the same ranking as a single minute (%d)", len(both.Groups), len(first.Groups))
	}
	for i, g := range both.Groups {
		want := len(first.Groups[i].Points) + len(second.Groups[i].Points)
		if g.Value != first.Groups[i].Value || len(g.Points) != want {
			t.Errorf("group %s: %d points, want %d", g.Value, len(g.Points), want)
		}
	}
}
