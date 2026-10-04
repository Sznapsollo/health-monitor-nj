package store_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/state"
	"github.com/Sznapsollo/health-monitor-nj/internal/store"
)

func TestActivityCountsStretchesNotEvents(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	day := base.UTC().Format("20060102")

	// Anna works for half an hour, takes a long break, then works again.
	var rows []store.LogRow
	for i := 0; i < 30; i++ {
		rows = append(rows, store.LogRow{
			Platform: "test", Signal: "dailyLogs", TS: base.Add(time.Duration(i) * time.Minute),
			Account: "acme", User: "anna",
		})
	}
	for i := 0; i < 10; i++ {
		rows = append(rows, store.LogRow{
			Platform: "test", Signal: "dailyLogs",
			TS:      base.Add(2*time.Hour + time.Duration(i)*time.Minute),
			Account: "acme", User: "anna",
		})
	}
	// Bob checks in once.
	rows = append(rows, store.LogRow{
		Platform: "test", Signal: "dailyLogs", TS: base, Account: "acme", User: "bob",
	})
	if err := s.WriteLogs(ctx, rows); err != nil {
		t.Fatal(err)
	}

	n, err := s.ComputeActivity(ctx, day)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if n != 2 {
		t.Fatalf("people = %d, want anna and bob", n)
	}

	got, err := s.ActivityFor(ctx, day)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("rows = %+v", got)
	}
	// 29 minutes of the first stretch plus 9 of the second; the two-hour gap
	// between them is not work.
	if got[0].User != "anna" || got[0].Minutes != 38 {
		t.Errorf("anna = %+v, want 38 minutes over two stretches", got[0])
	}
	if got[0].Events != 40 {
		t.Errorf("events = %d, want every log row counted", got[0].Events)
	}
	// A single event still counts as a minute, not as nothing.
	if got[1].User != "bob" || got[1].Minutes != 1 {
		t.Errorf("bob = %+v, want one minute", got[1])
	}
}

func TestActivityGapRule(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	day := base.UTC().Format("20060102")

	// Nine minutes apart is one stretch; eleven is two.
	if err := s.WriteLogs(ctx, []store.LogRow{
		{Platform: "test", Signal: "dailyLogs", TS: base, Account: "a", User: "close"},
		{Platform: "test", Signal: "dailyLogs", TS: base.Add(9 * time.Minute), Account: "a", User: "close"},
		{Platform: "test", Signal: "dailyLogs", TS: base, Account: "a", User: "apart"},
		{Platform: "test", Signal: "dailyLogs", TS: base.Add(11 * time.Minute), Account: "a", User: "apart"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ComputeActivity(ctx, day); err != nil {
		t.Fatal(err)
	}

	got, err := s.ActivityFor(ctx, day)
	if err != nil {
		t.Fatal(err)
	}
	byUser := map[string]int64{}
	for _, a := range got {
		byUser[a.User] = a.Minutes
	}
	if byUser["close"] != 9 {
		t.Errorf("close = %d minutes, want one 9-minute stretch", byUser["close"])
	}
	if byUser["apart"] != 2 {
		t.Errorf("apart = %d minutes, want two one-minute stretches", byUser["apart"])
	}
}

func TestRecomputingADayReplacesIt(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	day := base.UTC().Format("20060102")

	if err := s.WriteLogs(ctx, []store.LogRow{
		{Platform: "test", Signal: "dailyLogs", TS: base, Account: "a", User: "anna"},
	}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := s.ComputeActivity(ctx, day); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.ActivityFor(ctx, day)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("rows = %+v, want one per person no matter how often it runs", got)
	}
}

func TestPurgeActivity(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	if err := s.WriteLogs(ctx, []store.LogRow{
		{Platform: "test", Signal: "dailyLogs", TS: base.Add(-40 * 24 * time.Hour), Account: "a", User: "old"},
		{Platform: "test", Signal: "dailyLogs", TS: base, Account: "a", User: "new"},
	}); err != nil {
		t.Fatal(err)
	}
	oldDay := base.Add(-40 * 24 * time.Hour).UTC().Format("20060102")
	today := base.UTC().Format("20060102")
	if _, err := s.ComputeActivity(ctx, oldDay); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ComputeActivity(ctx, today); err != nil {
		t.Fatal(err)
	}

	if err := s.PurgeActivity(ctx, today); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.ActivityFor(ctx, oldDay); len(got) != 0 {
		t.Errorf("old activity survived the purge: %+v", got)
	}
	if got, _ := s.ActivityFor(ctx, today); len(got) != 1 {
		t.Errorf("today's activity was purged too")
	}
}

func TestBackupProducesAReadableCopy(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	write(t, s, sample(state.MinuteOf(base), map[string]string{"url": "/a"}, 3, 30))

	path := filepath.Join(t.TempDir(), "backup.db")
	if err := s.Backup(ctx, path); err != nil {
		t.Fatalf("backup: %v", err)
	}
	if info, err := os.Stat(path); err != nil || info.Size() == 0 {
		t.Fatalf("backup file = %v, %v", info, err)
	}

	// The copy opens as a database in its own right.
	copied, err := store.Open(store.Options{Dir: filepath.Dir(path)})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = copied.Close() }()
}

func TestActivityIsRefreshedSparinglyAndFrozenOnceTheDayIsOver(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	day := base.UTC().Format("20060102")
	if err := s.WriteLogs(ctx, []store.LogRow{{Platform: "test", Signal: "dailyLogs", TS: base, User: "anna", Account: "acme"}}); err != nil {
		t.Fatal(err)
	}

	steps := []struct {
		at   time.Time
		want bool
	}{
		{base, true},
		{base.Add(5 * time.Minute), false},
		{base.Add(11 * time.Minute), true},
		{base.Add(24 * time.Hour), true},
		{base.Add(48 * time.Hour), false},
	}
	for _, step := range steps {
		did, err := s.RefreshActivity(ctx, day, step.at)
		if err != nil {
			t.Fatal(err)
		}
		if did != step.want {
			t.Errorf("at %s: recomputed %v, want %v", step.at.Format(time.RFC3339), did, step.want)
		}
	}
}
