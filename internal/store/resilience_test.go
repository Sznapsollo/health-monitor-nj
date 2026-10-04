package store_test

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/state"
	"github.com/Sznapsollo/health-monitor-nj/internal/store"
)

func activityOf(t *testing.T, s *store.Store, day string) map[string]int64 {
	t.Helper()
	list, err := s.ActivityFor(context.Background(), day)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]int64{}
	for _, a := range list {
		out[a.User] = a.Minutes
	}
	return out
}

func TestActivityReadInPassesMatchesReadingItAtOnce(t *testing.T) {
	ctx := context.Background()
	day := time.Now().UTC()
	start := time.Date(day.Year(), day.Month(), day.Day(), 0, 30, 0, 0, time.UTC)
	key := start.Format("20060102")
	row := func(user string, at time.Duration) store.LogRow {
		return store.LogRow{Platform: "test", Signal: "dailyLogs", TS: start.Add(at), Account: "acme", User: user}
	}
	first := []store.LogRow{row("anna", 0), row("anna", 5*time.Minute), row("anna", 40*time.Minute), row("bob", 0)}
	late := []store.LogRow{row("anna", 25*time.Minute), row("anna", 15*time.Minute), row("bob", 2*time.Hour)}

	passes := open(t)
	if err := passes.WriteLogs(ctx, first); err != nil {
		t.Fatal(err)
	}
	if _, err := passes.ComputeActivity(ctx, key); err != nil {
		t.Fatal(err)
	}
	if err := passes.WriteLogs(ctx, late); err != nil {
		t.Fatal(err)
	}
	if _, err := passes.ComputeActivity(ctx, key); err != nil {
		t.Fatal(err)
	}

	once := open(t)
	if err := once.WriteLogs(ctx, append(append([]store.LogRow{}, first...), late...)); err != nil {
		t.Fatal(err)
	}
	if _, err := once.ComputeActivity(ctx, key); err != nil {
		t.Fatal(err)
	}

	got, want := activityOf(t, passes, key), activityOf(t, once, key)
	if got["anna"] != 26 || got["bob"] != 2 || got["anna"] != want["anna"] || got["bob"] != want["bob"] {
		t.Fatalf("in passes %v, at once %v; want anna 26 (the late rows join her first stretch) and bob 2", got, want)
	}
}

func TestAFinishedDayIsNotReadAgainAfterARestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := store.Open(store.Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	day := base.UTC().Format("20060102")
	if err := s.WriteLogs(ctx, []store.LogRow{{Platform: "test", Signal: "dailyLogs", TS: base, User: "anna", Account: "acme"}}); err != nil {
		t.Fatal(err)
	}
	if did, err := s.RefreshActivity(ctx, day, base.Add(48*time.Hour)); err != nil || !did {
		t.Fatalf("first pass: %v %v", did, err)
	}
	_ = s.Close()

	again, err := store.Open(store.Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = again.Close() })
	if did, err := again.RefreshActivity(ctx, day, base.Add(72*time.Hour)); err != nil || did {
		t.Fatalf("after a restart: read again %v (%v), want the finished day left alone", did, err)
	}
}

func TestLogRowsWaitOutALockedDatabase(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(store.Options{Dir: dir, BusyTimeout: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()
	if err := s.WriteLogs(ctx, []store.LogRow{{Platform: "test", Signal: "dailyLogs", TS: base, User: "warm"}}); err != nil {
		t.Fatal(err)
	}

	other, err := sql.Open("sqlite", filepath.Join(dir, "hm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Close() })
	conn, err := other.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		t.Fatal(err)
	}

	w := store.NewLogWriter(s, store.WriterOptions{FlushEvery: 10 * time.Millisecond, Log: slog.New(slog.NewTextHandler(discard{}, nil))})
	var rows []store.LogRow
	for i := range 100 {
		rows = append(rows, store.LogRow{Platform: "test", Signal: "dailyLogs", TS: base.Add(time.Duration(i) * time.Second), User: "anna"})
	}
	w.Write(rows)
	time.Sleep(300 * time.Millisecond)
	if _, err := conn.ExecContext(ctx, `ROLLBACK`); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()

	deadline := time.Now().Add(5 * time.Second)
	for w.Stats().Written < 100 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	w.Stop()
	if st := w.Stats(); st.Written != 100 || st.Errors != 0 {
		t.Fatalf("stats = %+v, want all 100 rows written once the lock went", st)
	}
	if n, err := s.CountLogs(ctx, base.UTC().Format("20060102")); err != nil || n != 101 {
		t.Fatalf("stored %d (%v), want 101", n, err)
	}
}

func TestABigDayIsDroppedCompletely(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	var rows []store.LogRow
	for i := range 12000 {
		rows = append(rows, store.LogRow{Platform: "test", Signal: "dailyLogs", TS: base.Add(time.Duration(i) * time.Second), User: "anna"})
	}
	if err := s.WriteLogs(ctx, rows); err != nil {
		t.Fatal(err)
	}
	tables, _ := s.LogTables(ctx)
	for _, tb := range tables {
		if err := s.DropLogTable(ctx, tb); err != nil {
			t.Fatal(err)
		}
	}
	if left, _ := s.LogTables(ctx); len(left) != 0 {
		t.Fatalf("tables left: %v", left)
	}
	var n int
	_ = s.DB().QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name LIKE 'log\_2%' ESCAPE '\'`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d log objects left in the database", n)
	}
}

func TestATableThatCannotBeWrittenLosesOnlyItsOwnRows(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	w := store.NewLogWriter(s, store.WriterOptions{FlushEvery: 10 * time.Millisecond, Log: slog.New(slog.NewTextHandler(discard{}, nil))})
	w.Write([]store.LogRow{
		{Platform: "test", Signal: "dailyLogs", TS: base, User: "anna"},
		{Platform: "test", Signal: "dailyLogs", TS: time.Date(33658, 1, 1, 0, 0, 0, 0, time.UTC), User: "far"},
		{Platform: "test", Signal: "sendLogs", TS: base, User: "bob"},
	})
	w.Stop()
	if st := w.Stats(); st.Written != 2 || st.Errors != 1 {
		t.Fatalf("stats = %+v, want the two good rows written and one error", st)
	}
	if n, err := s.CountLogs(ctx, base.UTC().Format("20060102")); err != nil || n != 2 {
		t.Fatalf("stored %d (%v), want 2", n, err)
	}
}

func TestAWriteRecreatesATableDroppedBehindItsBack(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	if err := s.WriteLogs(ctx, []store.LogRow{{Platform: "test", Signal: "dailyLogs", TS: base, User: "anna"}}); err != nil {
		t.Fatal(err)
	}
	tables, _ := s.LogTables(ctx)
	for _, tb := range tables {
		if _, err := s.DB().ExecContext(ctx, `DROP TABLE `+tb.FTSName()); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DB().ExecContext(ctx, `DROP TABLE `+tb.Name()); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.WriteLogs(ctx, []store.LogRow{{Platform: "test", Signal: "dailyLogs", TS: base, User: "bob"}}); err != nil {
		t.Fatal(err)
	}
	if n, err := s.CountLogs(ctx, base.UTC().Format("20060102")); err != nil || n != 1 {
		t.Fatalf("stored %d (%v), want 1", n, err)
	}
}

func TestRowsWrittenWhileADayIsArchivedAreNotLost(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	dir := t.TempDir()
	day := base.UTC().Format("20060102")
	const total = 400
	w := store.NewLogWriter(s, store.WriterOptions{FlushEvery: time.Millisecond, Queue: total, Log: slog.New(slog.NewTextHandler(discard{}, nil))})
	defer w.Stop()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range total {
			w.Write([]store.LogRow{{Platform: "test", Signal: "dailyLogs", TS: base.Add(time.Duration(i) * time.Millisecond), User: "anna"}})
			time.Sleep(50 * time.Microsecond)
		}
		deadline := time.Now().Add(5 * time.Second)
		for w.Stats().Written < total && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
	}()
	archive := func() {
		tables, _ := s.LogTables(ctx)
		for _, tb := range tables {
			if _, err := s.ArchiveLogTable(ctx, tb, dir); err != nil && !errors.Is(err, store.ErrArchiveMoved) {
				t.Error(err)
			}
		}
	}
	for running := true; running; {
		select {
		case <-done:
			running = false
		default:
			archive()
		}
	}
	archive()
	if st := w.Stats(); st.Written != total || st.Errors != 0 || st.Sampled != 0 {
		t.Fatalf("stats = %+v", st)
	}
	if n, _ := s.CountLogs(ctx, day); n != 0 {
		t.Fatalf("%d rows still in the database", n)
	}
	if got := len(readArchive(t, filepath.Join(dir, "logs-"+day+"-dailyLogs.ndjson.gz"))); got != total {
		t.Fatalf("archived %d rows, want %d", got, total)
	}
}

func TestWritesAfterStopAreCountedNotPanicked(t *testing.T) {
	s := open(t)
	quiet := store.WriterOptions{Log: slog.New(slog.NewTextHandler(discard{}, nil))}

	logs := store.NewLogWriter(s, quiet)
	logs.Stop()
	logs.Write([]store.LogRow{{Platform: "test", Signal: "dailyLogs", TS: base}})
	if st := logs.Stats(); st.Sampled != 1 {
		t.Errorf("log stats = %+v, want the late row counted", st)
	}

	alerts := store.NewAlertWriter(s, quiet)
	alerts.Stop()
	alerts.Write(store.AlertRow{ID: "a", Platform: "test", First: base, Last: base})
	if st := alerts.Stats(); st.Dropped != 1 {
		t.Errorf("alert stats = %+v, want the late row counted", st)
	}

	minutes := store.NewWriter(s, quiet)
	minutes.Stop()
	minutes.Samples([]state.Sample{sample(1, nil, 1, 1)})
	if st := minutes.Stats(); st.Dropped != 1 {
		t.Errorf("writer stats = %+v, want the late sample counted", st)
	}
}
