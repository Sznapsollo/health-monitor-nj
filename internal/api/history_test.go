package api_test

import (
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/api"
	"github.com/Sznapsollo/health-monitor-nj/internal/signal"
	"github.com/Sznapsollo/health-monitor-nj/internal/state"
	"github.com/Sznapsollo/health-monitor-nj/internal/store"
	"github.com/prometheus/client_golang/prometheus"
)

// A signal that keeps only five minutes of detail, so a test can reach past
// it without waiting two hours.
func historyFixture(t *testing.T) (*httptest.Server, *state.Store, *store.Store) {
	t.Helper()
	reg := signal.NewRegistry()
	err := reg.Add(&signal.Definition{
		Platform: "test", Name: "requests", Kind: signal.KindTimeseries,
		Dims:      []string{"url"},
		Values:    signal.Values{Count: "count", MS: "ms"},
		Retention: signal.Retention{HotDetailMinutes: 5, HotTotalsMinutes: 120, DurableDays: 30},
	})
	if err != nil {
		t.Fatal(err)
	}

	hot := state.NewStore(reg, func() time.Time { return now })
	db, err := store.Open(store.Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	srv := httptest.NewServer(api.Handler(api.Deps{
		Log:      slog.New(slog.NewTextHandler(discard{}, nil)),
		Metrics:  prometheus.NewRegistry(),
		Registry: reg,
		Store:    hot,
		DB:       db,
		Started:  now,
	}))
	t.Cleanup(srv.Close)
	return srv, hot, db
}

func writeRows(t *testing.T, db *store.Store, samples ...state.Sample) {
	t.Helper()
	w := store.NewWriter(db, store.WriterOptions{
		FlushEvery: time.Millisecond,
		Log:        slog.New(slog.NewTextHandler(discard{}, nil)),
	})
	w.Samples(samples)
	w.Stop()
}

func hotSample(minute int64, url string, count int64, ms float64) state.Sample {
	var a state.Agg
	a.Add(count, ms, true)
	return state.Sample{
		Platform: "test", Signal: "requests", Minute: minute,
		Dims: map[string]string{"url": url}, Agg: a,
	}
}

func getSeries(t *testing.T, srv *httptest.Server, query string) state.View {
	t.Helper()
	res, err := srv.Client().Get(srv.URL + "/api/series?" + query)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	var view state.View
	if err := json.NewDecoder(res.Body).Decode(&view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return view
}

func TestSeriesFallsBackToDiskForOlderMinutes(t *testing.T) {
	srv, hot, db := historyFixture(t)
	minute := state.MinuteOf(now)

	// In memory: the last two minutes. On disk: those plus an hour before.
	hot.Apply([]state.Sample{
		hotSample(minute, "/a", 3, 30),
		hotSample(minute-1, "/a", 2, 20),
	})
	writeRows(t, db,
		hotSample(minute, "/a", 3, 30),
		hotSample(minute-1, "/a", 2, 20),
		hotSample(minute-40, "/a", 10, 100),
		hotSample(minute-40, "/old", 7, 700),
	)

	// A window inside the detail tier is answered from memory alone.
	recent := getSeries(t, srv, "signal=requests&group=url&minutes=5")
	if recent.Partial {
		t.Errorf("a window inside the detail tier should not be partial")
	}
	if len(recent.Groups) != 1 || recent.Groups[0].Value != "/a" {
		t.Fatalf("groups = %+v", recent.Groups)
	}

	// A window reaching past it picks the older minutes up from SQLite.
	old := getSeries(t, srv, "signal=requests&group=url&minutes=60")
	values := map[string]int64{}
	for _, g := range old.Groups {
		values[g.Value] = g.Count
	}
	if values["/old"] != 7 {
		t.Fatalf("groups = %+v, want the url that only exists on disk", old.Groups)
	}
	if old.Partial {
		t.Error("the breakdown now covers the whole window, so it is not partial")
	}

	// The older points come first, in order, with the live ones after.
	var busy *state.Group
	for i := range old.Groups {
		if old.Groups[i].Value == "/a" {
			busy = &old.Groups[i]
		}
	}
	if busy == nil {
		t.Fatal("/a is missing")
	}
	minutes := make([]int64, 0, len(busy.Points))
	for _, p := range busy.Points {
		minutes = append(minutes, p.Minute)
	}
	if len(minutes) < 3 || minutes[0] != minute-40 {
		t.Fatalf("points = %v, want the disk minute first", minutes)
	}
	for i := 1; i < len(minutes); i++ {
		if minutes[i] <= minutes[i-1] {
			t.Fatalf("points = %v, want ascending minutes", minutes)
		}
	}
}

func TestSeriesWithoutADatabaseStillWorks(t *testing.T) {
	reg := signal.NewRegistry()
	if err := reg.Add(&signal.Definition{
		Platform: "test", Name: "requests", Kind: signal.KindTimeseries,
		Dims: []string{"url"}, Values: signal.Values{Count: "count", MS: "ms"},
	}); err != nil {
		t.Fatal(err)
	}
	hot := state.NewStore(reg, func() time.Time { return now })
	hot.Apply([]state.Sample{hotSample(state.MinuteOf(now), "/a", 1, 10)})

	srv := httptest.NewServer(api.Handler(api.Deps{
		Log:      slog.New(slog.NewTextHandler(discard{}, nil)),
		Metrics:  prometheus.NewRegistry(),
		Registry: reg,
		Store:    hot,
		Started:  now,
	}))
	defer srv.Close()

	view := getSeries(t, srv, "signal=requests&group=url&minutes=60")
	if len(view.Groups) != 1 {
		t.Fatalf("groups = %+v", view.Groups)
	}
}

func TestAFilteredViewCountsOnlyWhatTheFilterKeeps(t *testing.T) {
	srv, hot, db := historyFixture(t)
	minute := state.MinuteOf(now)
	hot.Apply([]state.Sample{
		hotSample(minute, "/api/orders", 3, 30),
		hotSample(minute, "/health", 50, 1),
	})
	writeRows(t, db,
		hotSample(minute, "/api/orders", 3, 30),
		hotSample(minute, "/health", 50, 1),
		hotSample(minute-40, "/api/orders", 10, 100),
		hotSample(minute-40, "/health", 70, 1),
	)

	total := func(v state.View) (n int64) {
		for _, p := range v.Total {
			n += p.Count
		}
		return n
	}
	all := getSeries(t, srv, "signal=requests&group=url&minutes=60")
	if total(all) != 133 || all.Filtered {
		t.Fatalf("unfiltered total = %d, want every request", total(all))
	}

	recent := getSeries(t, srv, "signal=requests&group=url&filter=orders&minutes=5")
	if !recent.Filtered || total(recent) != 3 {
		t.Errorf("filtered recent total = %d, want only /api/orders", total(recent))
	}

	older := getSeries(t, srv, "signal=requests&group=url&filter=orders&minutes=60")
	if total(older) != 13 {
		t.Errorf("filtered total over the hour = %d, want /api/orders from memory and from disk", total(older))
	}
	for _, g := range older.Groups {
		if g.Value != "/api/orders" {
			t.Errorf("group %q came back from disk although the filter drops it", g.Value)
		}
	}
}
