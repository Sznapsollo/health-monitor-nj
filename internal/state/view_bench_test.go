package state_test

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/signal"
	"github.com/Sznapsollo/health-monitor-nj/internal/state"
)

// benchSeries holds 120 detail minutes of 100 urls × 10 users.
func benchSeries(pair bool) (*state.Series, time.Time) {
	d := &signal.Definition{
		Platform: "p", Name: "requests", Kind: signal.KindTimeseries,
		Dims:      []string{"url", "user", "status"},
		Retention: signal.Retention{HotDetailMinutes: 120, HotTotalsMinutes: 1440},
		MaxKeys:   10000,
	}
	s := state.NewSeries(d)
	if pair {
		s.SetActivePairs([]state.Pair{{Main: "url", Sub: "user"}})
	}
	now := base.Add(200 * time.Minute)
	for m := 0; m < 120; m++ {
		at := now.Add(-time.Duration(m) * time.Minute)
		for u := 0; u < 100; u++ {
			for us := 0; us < 10; us++ {
				var a state.Agg
				a.Add(1, float64(u+us), true)
				dims := map[string]string{"url": fmt.Sprintf("/u/%d", u), "user": fmt.Sprintf("user%d", us), "status": "200"}
				s.Observe(state.MinuteOf(at), dims, a, at)
			}
		}
	}
	return s, now
}

func BenchmarkMinuteViewGroupSub(b *testing.B) {
	s, now := benchSeries(true)
	c := state.Criteria{Signal: "requests", Group: "url", Sub: "user"}
	b.ReportAllocs()
	for b.Loop() {
		_ = s.MinuteView(c, state.MinuteOf(now), now)
	}
}

func BenchmarkMinuteViewGroup(b *testing.B) {
	s, now := benchSeries(false)
	c := state.Criteria{Signal: "requests", Group: "url"}
	b.ReportAllocs()
	for b.Loop() {
		_ = s.MinuteView(c, state.MinuteOf(now), now)
	}
}

func BenchmarkMinuteViewTotals(b *testing.B) {
	s, now := benchSeries(false)
	c := state.Criteria{Signal: "requests"}
	b.ReportAllocs()
	for b.Loop() {
		_ = s.MinuteView(c, state.MinuteOf(now), now)
	}
}

func BenchmarkMarshalMinuteViewGroupSub(b *testing.B) {
	s, now := benchSeries(true)
	v := s.MinuteView(state.Criteria{Signal: "requests", Group: "url", Sub: "user"}, state.MinuteOf(now), now)
	out, _ := json.Marshal(v)
	b.ReportMetric(float64(len(out)), "bytes/msg")
	b.ReportAllocs()
	for b.Loop() {
		_, _ = json.Marshal(v)
	}
}

func BenchmarkEstimatedBytes(b *testing.B) {
	s, _ := benchSeries(true)
	b.ReportAllocs()
	for b.Loop() {
		_ = s.EstimatedBytes()
	}
}
