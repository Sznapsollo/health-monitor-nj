package state

import (
	"fmt"
	"testing"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/signal"
)

func walkKeys(s *Series) int {
	n := 0
	for _, m := range s.minutes {
		for _, byValue := range m.dims {
			n += len(byValue)
		}
		for _, byMain := range m.pairs {
			for _, bySub := range byMain {
				n += len(bySub)
			}
		}
	}
	return n
}

func TestRunningKeyCountMatchesTheMaps(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	s := NewSeries(&signal.Definition{
		Platform: "p", Name: "requests", Kind: signal.KindTimeseries,
		Dims:      []string{"url", "user"},
		Retention: signal.Retention{HotDetailMinutes: 10, HotTotalsMinutes: 30},
		MaxKeys:   5,
	})
	s.SetActivePairs([]Pair{{Main: "url", Sub: "user"}})
	check := func(step string) {
		t.Helper()
		if got, want := s.detailKeys, walkKeys(s); got != want {
			t.Fatalf("%s: running count %d, maps hold %d", step, got, want)
		}
	}

	var a Agg
	a.Add(1, 10, true)
	for m := 0; m < 20; m++ {
		at := now.Add(-time.Duration(m) * time.Minute)
		for i := 0; i < 8; i++ {
			s.Observe(MinuteOf(at), map[string]string{"url": fmt.Sprintf("/%d", i), "user": fmt.Sprintf("u%d", i%3)}, a, now)
		}
	}
	check("observe, past the cap")

	s.Restore(RestoredMinute{
		Minute: MinuteOf(now.Add(-25 * time.Minute)),
		Dims:   map[string]map[string]Agg{"url": {"/r": a}},
	}, now)
	s.Restore(RestoredMinute{
		Minute: MinuteOf(now.Add(time.Minute)),
		Dims:   map[string]map[string]Agg{"url": {"/r": a, "/s": a}},
		Pairs:  map[Pair]map[string]map[string]Agg{{Main: "url", Sub: "user"}: {"/r": {"u1": a}}},
	}, now)
	check("restore")

	s.EvictOldestDetail()
	check("evict")

	s.Maintain(now.Add(15 * time.Minute))
	check("maintain")
}
