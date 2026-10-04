package store_test

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/signal"
	"github.com/Sznapsollo/health-monitor-nj/internal/state"
	"github.com/Sznapsollo/health-monitor-nj/internal/store"
)

var base = time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

func open(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(store.Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func sample(minute int64, dims map[string]string, count int64, ms float64) state.Sample {
	var a state.Agg
	a.Add(count, ms, true)
	return state.Sample{Platform: "test", Signal: "requests", Minute: minute, Dims: dims, Agg: a}
}

func write(t *testing.T, s *store.Store, samples ...state.Sample) {
	t.Helper()
	w := store.NewWriter(s, store.WriterOptions{
		FlushEvery: time.Millisecond,
		Log:        slog.New(slog.NewTextHandler(discard{}, nil)),
	})
	w.Samples(samples)
	w.Stop()
}

func writeWith(t *testing.T, s *store.Store, layout store.Layout, samples ...state.Sample) {
	t.Helper()
	w := store.NewWriter(s, store.WriterOptions{
		FlushEvery: time.Millisecond,
		Log:        slog.New(slog.NewTextHandler(discard{}, nil)),
		Layout:     func(string, string) (store.Layout, bool) { return layout, true },
	})
	w.Samples(samples)
	w.Stop()
}

func TestWriteAndReadBack(t *testing.T) {
	s := open(t)
	minute := state.MinuteOf(base)
	write(t, s,
		sample(minute, map[string]string{"url": "/a"}, 1, 100),
		sample(minute, map[string]string{"url": "/b"}, 1, 300),
	)

	rows, err := s.Series(context.Background(), store.Query{
		Platform: "test", Signal: "requests", From: minute - 5, To: minute,
	})
	if err != nil {
		t.Fatalf("series: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want one per minute", rows)
	}
	if rows[0].Agg.Count != 2 || rows[0].Agg.AvgMS() != 200 {
		t.Errorf("row = %+v, avg = %v", rows[0], rows[0].Agg.AvgMS())
	}
	if rows[0].Agg.MinMS != 100 || rows[0].Agg.MaxMS != 300 {
		t.Errorf("min/max = %v/%v", rows[0].Agg.MinMS, rows[0].Agg.MaxMS)
	}
}

func TestRepeatedFlushesAccumulate(t *testing.T) {
	s := open(t)
	minute := state.MinuteOf(base)
	dims := map[string]string{"url": "/a"}

	// The readers flush the same minute several times a second; the row has
	// to add up rather than be replaced.
	for i := 0; i < 5; i++ {
		write(t, s, sample(minute, dims, 2, 50))
	}
	rows, err := s.Series(context.Background(), store.Query{
		Platform: "test", Signal: "requests", From: minute, To: minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].Agg.Count != 10 {
		t.Errorf("count = %d, want every flush added", rows[0].Agg.Count)
	}
	if rows[0].Agg.Samples != 5 {
		t.Errorf("samples = %d", rows[0].Agg.Samples)
	}
}

func TestDurationsWidenOnlyWithMeasurements(t *testing.T) {
	s := open(t)
	minute := state.MinuteOf(base)
	dims := map[string]string{"url": "/a"}

	write(t, s, sample(minute, dims, 1, 200))
	// A signal with no duration must not drag the minimum down to zero.
	var countOnly state.Agg
	countOnly.Add(3, 0, false)
	write(t, s, state.Sample{Platform: "test", Signal: "requests", Minute: minute, Dims: dims, Agg: countOnly})

	rows, err := s.Series(context.Background(), store.Query{
		Platform: "test", Signal: "requests", From: minute, To: minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].Agg.Count != 4 {
		t.Errorf("count = %d", rows[0].Agg.Count)
	}
	if rows[0].Agg.MinMS != 200 || rows[0].Agg.MaxMS != 200 {
		t.Errorf("min/max = %v/%v, want the one real measurement", rows[0].Agg.MinMS, rows[0].Agg.MaxMS)
	}
}

func TestSeriesGroupedByDimension(t *testing.T) {
	s := open(t)
	minute := state.MinuteOf(base)
	write(t, s,
		sample(minute, map[string]string{"url": "/a", "user": "anna"}, 3, 10),
		sample(minute, map[string]string{"url": "/a", "user": "bob"}, 1, 20),
		sample(minute, map[string]string{"url": "/b", "user": "anna"}, 5, 30),
	)

	rows, err := s.Series(context.Background(), store.Query{
		Platform: "test", Signal: "requests", From: minute, To: minute, Group: "url",
	})
	if err != nil {
		t.Fatalf("series: %v", err)
	}
	got := map[string]int64{}
	for _, r := range rows {
		got[r.Value] = r.Agg.Count
	}
	if got["/a"] != 4 || got["/b"] != 5 {
		t.Fatalf("grouped = %+v", got)
	}

	// Grouping by the other dimension of the same rows works too.
	rows, err = s.Series(context.Background(), store.Query{
		Platform: "test", Signal: "requests", From: minute, To: minute, Group: "user",
	})
	if err != nil {
		t.Fatal(err)
	}
	got = map[string]int64{}
	for _, r := range rows {
		got[r.Value] = r.Agg.Count
	}
	if got["anna"] != 8 || got["bob"] != 1 {
		t.Fatalf("grouped by user = %+v", got)
	}
}

func TestLoadRestoresBothTiers(t *testing.T) {
	s := open(t)
	now := base
	minute := state.MinuteOf(now)

	// Three minutes of detail and one older minute that is past it.
	for i := int64(0); i < 3; i++ {
		write(t, s, sample(minute-i, map[string]string{"url": "/a"}, 1, 100))
	}
	write(t, s, sample(minute-30, map[string]string{"url": "/old"}, 7, 700))

	minutes, err := s.Load(context.Background(), store.Restore{
		Platform: "test", Signal: "requests",
		DetailFrom: minute - 2, TotalsFrom: minute - 60, To: minute,
	})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(minutes) != 4 {
		t.Fatalf("minutes = %d, want three detailed and one with its total only", len(minutes))
	}
	var withDims int
	for _, m := range minutes {
		if len(m.Dims) > 0 {
			withDims++
		}
	}
	if withDims != 3 {
		t.Errorf("minutes with values = %d, want 3", withDims)
	}

	hot := newHotStore(t, now)
	if !hot.Restore("test", "requests", minutes) {
		t.Fatal("restore refused a known signal")
	}
	view, ok := hot.View("test", "requests", state.Criteria{HistoryMinutes: 60, Group: "url"})
	if !ok {
		t.Fatal("no view after restore")
	}
	var total int64
	for _, p := range view.Total {
		total += p.Count
	}
	if total != 10 {
		t.Errorf("restored total = %d, want 3 + 7", total)
	}
	if len(view.Groups) != 1 || view.Groups[0].Value != "/a" || view.Groups[0].Count != 3 {
		t.Errorf("groups = %+v, want only the detailed minutes broken down", view.Groups)
	}
}

func TestStoresPerDimensionNotPerCombination(t *testing.T) {
	s := open(t)
	minute := state.MinuteOf(base)
	var samples []state.Sample
	for i := 0; i < 1000; i++ {
		samples = append(samples, sample(minute, map[string]string{
			"url":  fmt.Sprintf("/u%d", i%10),
			"user": fmt.Sprintf("user-%d", i),
			"port": fmt.Sprintf("80%d", i%5),
		}, 1, 10))
	}
	writeWith(t, s, store.Layout{
		Dims:    []string{"url", "user", "port"},
		Pairs:   []state.Pair{{Main: "port", Sub: "url"}},
		MaxKeys: 100,
	}, samples...)

	counts, err := s.Counts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// 1 total + 10 urls + 101 users (100 kept, the rest in one other row)
	// + 5 ports + 10 port/url pairs (each url always on one port), instead
	// of 1000 combinations.
	if got := counts["test/requests"]; got != 1+10+101+5+10 {
		t.Errorf("rows = %d, want 127", got)
	}
	rows, err := s.Series(context.Background(), store.Query{
		Platform: "test", Signal: "requests", From: minute, To: minute, Group: "user",
	})
	if err != nil {
		t.Fatal(err)
	}
	var sum, other int64
	for _, r := range rows {
		sum += r.Agg.Count
		if r.Value == state.OtherBucket {
			other = r.Agg.Count
		}
	}
	if sum != 1000 || other != 900 {
		t.Errorf("users add up to %d with %d in other, want 1000 and 900", sum, other)
	}
}

func TestPairsAreRestoredWhenKept(t *testing.T) {
	s := open(t)
	minute := state.MinuteOf(base)
	pair := state.Pair{Main: "url", Sub: "user"}
	writeWith(t, s, store.Layout{Dims: []string{"url", "user"}, Pairs: []state.Pair{pair}},
		sample(minute, map[string]string{"url": "/a", "user": "anna"}, 2, 10),
		sample(minute, map[string]string{"url": "/a", "user": "bob"}, 1, 10),
	)
	minutes, err := s.Load(context.Background(), store.Restore{
		Platform: "test", Signal: "requests", DetailFrom: minute, TotalsFrom: minute, To: minute,
		Pairs: []state.Pair{pair},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(minutes) != 1 || minutes[0].Pairs[pair]["/a"]["anna"].Count != 2 {
		t.Fatalf("minutes = %+v", minutes)
	}
}

func TestRollupSumsOldValuesPerHour(t *testing.T) {
	s := open(t)
	hour := state.MinuteOf(base) - state.MinuteOf(base)%60 - 24*60
	for i := int64(0); i < 60; i++ {
		write(t, s, sample(hour+i, map[string]string{"url": "/a"}, 1, float64(10+i)))
	}
	n, err := s.Rollup(context.Background(), "test", "requests", hour+90)
	if err != nil {
		t.Fatalf("rollup: %v", err)
	}
	if n != 60 {
		t.Errorf("rolled up %d rows, want the hour's 60", n)
	}
	rows, err := s.Series(context.Background(), store.Query{
		Platform: "test", Signal: "requests", From: hour, To: hour + 59, Group: "url",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Minute != hour || rows[0].Agg.Count != 60 ||
		rows[0].Agg.MinMS != 10 || rows[0].Agg.MaxMS != 69 {
		t.Fatalf("hourly rows = %+v", rows)
	}
	totals, err := s.Series(context.Background(), store.Query{
		Platform: "test", Signal: "requests", From: hour, To: hour + 59,
	})
	if err != nil || len(totals) != 60 {
		t.Errorf("totals stay per minute: %d rows, %v", len(totals), err)
	}
}

func TestPurgeAndOldest(t *testing.T) {
	s := open(t)
	minute := state.MinuteOf(base)
	write(t, s,
		sample(minute-100, map[string]string{"url": "/old"}, 1, 10),
		sample(minute, map[string]string{"url": "/new"}, 1, 10),
	)

	oldest, err := s.Oldest(context.Background(), "test", "requests")
	if err != nil {
		t.Fatal(err)
	}
	if oldest != minute-100 {
		t.Errorf("oldest = %d", oldest)
	}

	n, err := s.Purge(context.Background(), "test", "requests", minute-50)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if n != 2 {
		t.Errorf("purged = %d, want the old minute's total and its one value", n)
	}
	oldest, _ = s.Oldest(context.Background(), "test", "requests")
	if oldest != minute {
		t.Errorf("oldest after purge = %d", oldest)
	}
}

func TestWriterDropsRatherThanBlocks(t *testing.T) {
	s := open(t)
	w := store.NewWriter(s, store.WriterOptions{
		FlushEvery: time.Hour, // never flushes on its own during the test
		Queue:      1,
		Log:        slog.New(slog.NewTextHandler(discard{}, nil)),
	})
	defer w.Stop()

	minute := state.MinuteOf(base)
	for i := 0; i < 100; i++ {
		w.Samples([]state.Sample{sample(minute, map[string]string{"url": "/a"}, 1, 10)})
	}
	if w.Stats().Dropped == 0 {
		t.Fatal("a full queue should drop with a counter, not block the readers")
	}
}

func TestDatabaseSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	minute := state.MinuteOf(base)

	first, err := store.Open(store.Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	write(t, first, sample(minute, map[string]string{"url": "/a"}, 5, 10))
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := store.Open(store.Options{Dir: dir})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = second.Close() }()

	rows, err := second.Series(context.Background(), store.Query{
		Platform: "test", Signal: "requests", From: minute, To: minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Agg.Count != 5 {
		t.Fatalf("rows = %+v, want what was written before the restart", rows)
	}
	if filepath.Base(second.Path()) != "hm.db" {
		t.Errorf("path = %q", second.Path())
	}
}

func TestCounts(t *testing.T) {
	s := open(t)
	minute := state.MinuteOf(base)
	write(t, s,
		sample(minute, map[string]string{"url": "/a"}, 1, 10),
		sample(minute, map[string]string{"url": "/b"}, 1, 10),
	)
	counts, err := s.Counts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if counts["test/requests"] != 3 {
		t.Fatalf("counts = %+v, want one total and two values", counts)
	}
}

func newHotStore(t *testing.T, now time.Time) *state.Store {
	t.Helper()
	reg := signalRegistry(t)
	return state.NewStore(reg, func() time.Time { return now })
}

func signalRegistry(t *testing.T) *signal.Registry {
	t.Helper()
	reg := signal.NewRegistry()
	err := reg.Add(&signal.Definition{
		Platform: "test", Name: "requests", Kind: signal.KindTimeseries,
		Dims:      []string{"url", "user"},
		Values:    signal.Values{Count: "count", MS: "ms"},
		Retention: signal.Retention{HotDetailMinutes: 5, HotTotalsMinutes: 120, DurableDays: 30},
	})
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func TestAMinuteIsWrittenOnceNotPerBatch(t *testing.T) {
	s := open(t)
	w := store.NewWriter(s, store.WriterOptions{
		FlushEvery: time.Hour,
		Log:        slog.New(slog.NewTextHandler(discard{}, nil)),
	})
	minute := state.MinuteOf(base)
	for i := 0; i < 200; i++ {
		w.Samples([]state.Sample{sample(minute, map[string]string{"url": "/a"}, 1, 10)})
	}
	w.Samples([]state.Sample{sample(minute+1, map[string]string{"url": "/a"}, 1, 10)})

	deadline := time.Now().Add(5 * time.Second)
	for w.Stats().Batches == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := w.Stats().Batches; got != 1 {
		t.Fatalf("batches = %d, want the closed minute written once when the next began", got)
	}
	w.Stop()

	var count int64
	if err := s.DB().QueryRow(`SELECT count FROM agg_total WHERE minute = ?`, minute).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 200 {
		t.Errorf("count = %d, want all 200 samples of the minute", count)
	}
	if got := w.Stats().Written; got != 201 {
		t.Errorf("written = %d, want every sample accounted for", got)
	}
}
