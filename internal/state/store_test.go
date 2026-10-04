package state_test

import (
	"sync"
	"testing"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/signal"
	"github.com/Sznapsollo/health-monitor-nj/internal/state"
)

func testStore(t *testing.T, now time.Time) (*state.Store, *signal.Registry) {
	t.Helper()
	reg := signal.NewRegistry()
	if err := reg.Add(testDef("url", "user")); err != nil {
		t.Fatal(err)
	}
	return state.NewStore(reg, func() time.Time { return now }), reg
}

func sample(minute int64, dims map[string]string, ms float64) state.Sample {
	var a state.Agg
	a.Add(1, ms, true)
	return state.Sample{Platform: "test", Signal: "requests", Minute: minute, Dims: dims, Agg: a}
}

func TestStoreApply(t *testing.T) {
	now := base
	s, _ := testStore(t, now)
	min := state.MinuteOf(now)

	dirty, unknown := s.Apply([]state.Sample{
		sample(min, map[string]string{"url": "/a"}, 100),
		sample(min, map[string]string{"url": "/b"}, 300),
		sample(min-1, map[string]string{"url": "/a"}, 50),
	})
	if len(unknown) != 0 {
		t.Fatalf("unknown = %+v", unknown)
	}
	if len(dirty) != 2 {
		t.Fatalf("dirty = %+v, want one entry per changed minute", dirty)
	}

	v, ok := s.View("test", "requests", state.Criteria{HistoryMinutes: 10, Group: "url"})
	if !ok {
		t.Fatal("no view for a signal that was just fed")
	}
	if len(v.Total) != 2 {
		t.Fatalf("total = %+v", v.Total)
	}
	if v.Groups[0].Value != "/a" || v.Groups[0].Count != 2 {
		t.Errorf("groups = %+v, want /a with 2", v.Groups)
	}
}

func TestStoreLabelsGroupsByTheirName(t *testing.T) {
	reg := signal.NewRegistry()
	def := testDef("account", "accountName", "port")
	def.Display.Labels = map[string]string{"account": "accountName"}
	def.KeepPairs = [][2]string{{"port", "account"}}
	if err := reg.Add(def); err != nil {
		t.Fatal(err)
	}
	s := state.NewStore(reg, func() time.Time { return base })
	min := state.MinuteOf(base)
	s.Apply([]state.Sample{
		sample(min, map[string]string{"account": "42", "accountName": "Acme", "port": "80"}, 10),
		sample(min, map[string]string{"account": "7", "port": "80"}, 10),
	})

	v, _ := s.View("test", "requests", state.Criteria{HistoryMinutes: 10, Group: "account"})
	labels := map[string]string{}
	for _, g := range v.Groups {
		labels[g.Value] = g.Label
	}
	if labels["42"] != "Acme" || labels["7"] != "" {
		t.Errorf("labels = %v, want 42 named Acme and 7 left bare", labels)
	}

	v, _ = s.View("test", "requests", state.Criteria{HistoryMinutes: 10, Group: "account", GroupFilter: "acme"})
	if len(v.Groups) != 1 || v.Groups[0].Value != "42" {
		t.Errorf("filtered groups = %+v, want the filter to match the name", v.Groups)
	}

	v, _ = s.View("test", "requests", state.Criteria{HistoryMinutes: 10, Group: "port", Sub: "account"})
	if subs := v.Groups[0].Groups; len(subs) != 2 || subs[0].Label+subs[1].Label != "Acme" {
		t.Errorf("sub-groups = %+v, want the name on 42", subs)
	}
	if got := s.Label("test", "requests", "account", "42"); got != "Acme" {
		t.Errorf("Label = %q", got)
	}
}

func TestStoreQuarantinesUnknownSignals(t *testing.T) {
	s, _ := testStore(t, base)
	in := state.Sample{Platform: "test", Signal: "mystery", Minute: state.MinuteOf(base)}
	dirty, unknown := s.Apply([]state.Sample{in})
	if len(dirty) != 0 {
		t.Errorf("dirty = %+v, want none", dirty)
	}
	if len(unknown) != 1 || unknown[0].Signal != "mystery" {
		t.Fatalf("unknown = %+v, want the mystery sample handed back", unknown)
	}
	if _, ok := s.View("test", "mystery", state.Criteria{}); ok {
		t.Error("a view exists for a signal the registry does not know")
	}
}

func TestStorePicksUpAutoRegisteredSignals(t *testing.T) {
	s, reg := testStore(t, base)
	reg.SetAutoRegister("test", true)
	if _, ok := reg.LookupOrRegister("test", "invented", signal.KindTimeseries, []string{"url"}); !ok {
		t.Fatal("auto-registration failed")
	}
	dirty, unknown := s.Apply([]state.Sample{{
		Platform: "test", Signal: "invented", Minute: state.MinuteOf(base),
		Dims: map[string]string{"url": "/x"}, Agg: func() state.Agg {
			var a state.Agg
			a.Add(1, 5, true)
			return a
		}(),
	}})
	if len(unknown) != 0 || len(dirty) != 1 {
		t.Fatalf("dirty = %+v, unknown = %+v", dirty, unknown)
	}
	if _, ok := s.View("test", "invented", state.Criteria{}); !ok {
		t.Error("no view for the auto-registered signal")
	}
}

func TestStoreActivePairs(t *testing.T) {
	now := base
	s, _ := testStore(t, now)
	min := state.MinuteOf(now)
	c := state.Criteria{HistoryMinutes: 10, Group: "url", Sub: "user"}
	pair, _ := c.Pair()

	s.Apply([]state.Sample{sample(min, map[string]string{"url": "/a", "user": "alice"}, 10)})
	s.SetActivePairs("test", "requests", []state.Pair{pair})
	s.Apply([]state.Sample{sample(min, map[string]string{"url": "/a", "user": "bob"}, 10)})

	v, _ := s.View("test", "requests", c)
	subs := v.Groups[0].Groups
	if len(subs) != 1 || subs[0].Value != "bob" {
		t.Fatalf("sub-groups = %+v, want only what arrived after the pair went active", subs)
	}
}

func TestStoreMinuteView(t *testing.T) {
	now := base
	s, _ := testStore(t, now)
	min := state.MinuteOf(now)
	s.Apply([]state.Sample{
		sample(min-1, map[string]string{"url": "/a"}, 10),
		sample(min, map[string]string{"url": "/a"}, 10),
	})
	v, ok := s.MinuteView("test", "requests", state.Criteria{HistoryMinutes: 10, Group: "url"}, min)
	if !ok {
		t.Fatal("no minute view")
	}
	if len(v.Total) != 1 || v.Total[0].Minute != min {
		t.Fatalf("total = %+v, want just the one minute", v.Total)
	}
}

func TestStoreIsSafeUnderConcurrentUse(t *testing.T) {
	now := base
	s, _ := testStore(t, now)
	min := state.MinuteOf(now)

	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 250; i++ {
				s.Apply([]state.Sample{sample(min, map[string]string{"url": "/a", "user": "alice"}, 10)})
			}
		}()
	}
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 250; i++ {
				s.View("test", "requests", state.Criteria{HistoryMinutes: 10, Group: "url"})
				s.Stats()
				s.Maintain()
			}
		}()
	}
	wg.Wait()

	v, _ := s.View("test", "requests", state.Criteria{HistoryMinutes: 10})
	if v.Total[0].Count != 2000 {
		t.Fatalf("count = %d, want every observation counted once", v.Total[0].Count)
	}
}

func TestStoreStats(t *testing.T) {
	s, _ := testStore(t, base)
	s.Apply([]state.Sample{sample(state.MinuteOf(base), map[string]string{"url": "/a"}, 10)})
	stats := s.Stats()
	got, ok := stats["test/requests"]
	if !ok {
		t.Fatalf("stats = %+v, want a test/requests entry", stats)
	}
	if got.Minutes != 1 || got.DetailMinutes != 1 || got.DimensionKeys != 1 {
		t.Errorf("stats = %+v", got)
	}
	if ps := s.Platforms(); len(ps) != 1 || ps[0] != "test" {
		t.Errorf("platforms = %v", ps)
	}
}

func TestMemoryGuardEvictsDetailNotTotals(t *testing.T) {
	now := base
	s, _ := testStore(t, now)
	minute := state.MinuteOf(now)

	for i := int64(0); i < 5; i++ {
		for u := 0; u < 20; u++ {
			s.Apply([]state.Sample{sample(minute-i, map[string]string{
				"url": "/u" + string(rune('a'+u%26)), "user": "anna",
			}, 10)})
		}
	}
	full := s.EstimatedBytes()
	if full == 0 {
		t.Fatal("the estimate is zero with data loaded")
	}

	// A limit below what is held forces the guard to act.
	s.SetMaxHotBytes(full / 2)
	s.Maintain()

	if s.Evictions() == 0 {
		t.Fatal("the guard did not evict anything")
	}
	if after := s.EstimatedBytes(); after > full/2/10*9 {
		t.Errorf("estimate = %d after the guard ran, want at most nine tenths of the %d limit", after, full/2)
	}

	// Every minute still counts, because only breakdowns were dropped.
	v, _ := s.View("test", "requests", state.Criteria{HistoryMinutes: 30})
	if len(v.Total) != 5 {
		t.Fatalf("total points = %d, want every minute kept", len(v.Total))
	}
	var count int64
	for _, p := range v.Total {
		count += p.Count
	}
	if count != 100 {
		t.Errorf("count = %d, want all 100 observations", count)
	}
}

func TestMemoryGuardSkipsSignalsWithNothingToDrop(t *testing.T) {
	reg := signal.NewRegistry()
	requests := testDef("url")
	jobs := testDef("url")
	jobs.Name = "jobs"
	jobs.Retention.HotTotalsMinutes = 5000
	for _, d := range []*signal.Definition{requests, jobs} {
		if err := reg.Add(d); err != nil {
			t.Fatal(err)
		}
	}
	s := state.NewStore(reg, func() time.Time { return base })
	now := state.MinuteOf(base)

	// jobs is the bigger signal, but every minute of it is already totals only.
	for i := int64(10); i < 4000; i++ {
		smp := sample(now-i, map[string]string{"url": "/j"}, 1)
		smp.Signal = "jobs"
		s.Apply([]state.Sample{smp})
	}
	for i := int64(0); i < 5; i++ {
		for u := 0; u < 20; u++ {
			s.Apply([]state.Sample{sample(now-i, map[string]string{"url": "/u" + string(rune('a'+u))}, 10)})
		}
	}
	withDetail := s.Stats()["test/requests"].EstimatedBytes
	if s.Stats()["test/jobs"].EstimatedBytes <= withDetail {
		t.Fatal("fixture: jobs should be the larger signal")
	}

	s.SetMaxHotBytes(s.EstimatedBytes() - withDetail/2)
	s.Maintain()
	if got := s.Stats()["test/requests"].DetailMinutes; got == 5 {
		t.Errorf("detail minutes = %d, want the guard to reach past the larger totals-only signal", got)
	}
}

func TestMemoryGuardOffByDefault(t *testing.T) {
	s, _ := testStore(t, base)
	s.Apply([]state.Sample{sample(state.MinuteOf(base), map[string]string{"url": "/a"}, 10)})
	s.Maintain()
	if s.Evictions() != 0 {
		t.Errorf("the guard ran while switched off")
	}
}

func TestReadingAnUnknownPlatformInventsNothing(t *testing.T) {
	s, _ := testStore(t, base)
	s.Apply([]state.Sample{sample(state.MinuteOf(base), map[string]string{"url": "/a"}, 10)})

	// A chart asking about a platform that does not exist gets nothing, and
	// crucially does not bring it into being: an invented empty name would
	// then sort first in every list and become the default everywhere.
	if _, ok := s.View("", "requests", state.Criteria{}); ok {
		t.Error("a view was returned for an empty platform")
	}
	if _, ok := s.View("nonexistent", "requests", state.Criteria{}); ok {
		t.Error("a view was returned for an unknown platform")
	}
	if _, ok := s.MinuteView("", "requests", state.Criteria{}, state.MinuteOf(base)); ok {
		t.Error("a minute view was returned for an empty platform")
	}
	s.SetActivePairs("", "requests", []state.Pair{{Main: "url", Sub: "user"}})

	if got := s.Platforms(); len(got) != 1 || got[0] != "test" {
		t.Fatalf("platforms = %v, want only the one that has data", got)
	}
}

func TestAddActivePairKeepsWhatOthersAskedFor(t *testing.T) {
	now := base
	s, _ := testStore(t, now)
	byUser := state.Pair{Main: "url", Sub: "user"}
	byURL := state.Pair{Main: "user", Sub: "url"}

	s.SetActivePairs("test", "requests", []state.Pair{byUser})
	s.AddActivePair("test", "requests", byURL)
	s.AddActivePair("test", "requests", byURL) // twice is still once

	min := state.MinuteOf(now)
	s.Apply([]state.Sample{sample(min, map[string]string{"url": "/a", "user": "alice"}, 10)})
	for _, c := range []state.Criteria{
		{HistoryMinutes: 10, Group: "url", Sub: "user"},
		{HistoryMinutes: 10, Group: "user", Sub: "url"},
	} {
		v, _ := s.View("test", "requests", c)
		if len(v.Groups) != 1 || len(v.Groups[0].Groups) != 1 {
			t.Fatalf("%s by %s: groups = %+v, want both pairs kept", c.Group, c.Sub, v.Groups)
		}
	}
}

func TestAskingForPairsOfUnknownSignalsKeepsNothing(t *testing.T) {
	s, _ := testStore(t, base)
	pair := state.Pair{Main: "url", Sub: "user"}
	s.AddActivePair("random-1", "requests", pair)
	s.SetActivePairs("random-2", "requests", []state.Pair{pair})
	s.AddActivePair("test", "random", pair)
	if got := s.Platforms(); len(got) != 0 {
		t.Fatalf("platforms = %v, want none invented", got)
	}

	s.AddActivePair("test", "requests", pair)
	if got := s.ActivePairSignals("test"); len(got) != 1 || got[0] != "requests" {
		t.Fatalf("active = %v, want only the defined signal", got)
	}
}
