package core_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/core"
	"github.com/Sznapsollo/health-monitor-nj/internal/signal"
	"github.com/Sznapsollo/health-monitor-nj/internal/state"
	"github.com/Sznapsollo/health-monitor-nj/internal/store"
)

// A day of one kind of log, written straight to the store.
func logDay(t *testing.T, db *store.Store, at time.Time, kind string) {
	t.Helper()
	err := db.WriteLogs(context.Background(), []store.LogRow{{
		Platform: "test", Signal: kind, TS: at, User: "anna", Account: "acme",
		Payload: map[string]any{"x": 1},
	}})
	if err != nil {
		t.Fatal(err)
	}
}

func TestEachKindOfLogExpiresOnItsOwnClock(t *testing.T) {
	c := &clock{t: base}
	db, err := store.Open(store.Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	reg := signal.NewRegistry()
	brain := core.New(state.NewStore(reg, c.now), reg,
		slog.New(slog.NewTextHandler(discard{}, nil)), c.now)
	// The bulky kind for less time than the rest.
	brain.SetLogging(core.Logging{
		KeepDays:       14,
		KeepDaysByKind: map[string]int{"dailyLogs": 5},
		AlertDays:      14,
	})

	// Eight days ago: past dailyLogs' five, inside everything else's fourteen.
	eightDaysAgo := base.AddDate(0, 0, -8)
	logDay(t, db, eightDaysAgo, "dailyLogs")
	logDay(t, db, eightDaysAgo, "sendLogs")
	logDay(t, db, base, "dailyLogs")

	brain.Rollover(context.Background(), db)

	tables, err := db.LogTables(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	kept := map[string]bool{}
	for _, tb := range tables {
		kept[tb.Day+"/"+tb.Signal] = true
	}
	old := eightDaysAgo.UTC().Format("20060102")
	if kept[old+"/dailyLogs"] {
		t.Errorf("tables = %v, want the bulky kind dropped after its five days", kept)
	}
	if !kept[old+"/sendLogs"] {
		t.Errorf("tables = %v, want the other kinds kept for their fourteen", kept)
	}
	if !kept[base.UTC().Format("20060102")+"/dailyLogs"] {
		t.Errorf("tables = %v, want today untouched", kept)
	}
}

func TestAlertHistoryIsPurgedByTheRollover(t *testing.T) {
	c := &clock{t: base}
	db, err := store.Open(store.Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	reg := signal.NewRegistry()
	brain := core.New(state.NewStore(reg, c.now), reg,
		slog.New(slog.NewTextHandler(discard{}, nil)), c.now)
	brain.SetLogging(core.Logging{KeepDays: 14, AlertDays: 14})

	ctx := context.Background()
	old := base.AddDate(0, 0, -20)
	err = db.WriteAlerts(ctx, []store.AlertRow{
		{ID: "old", Platform: "test", Level: "ERROR", Message: "ancient", Count: 1, First: old, Last: old},
		{ID: "new", Platform: "test", Level: "ERROR", Message: "today", Count: 1, First: base, Last: base},
	})
	if err != nil {
		t.Fatal(err)
	}

	brain.Rollover(ctx, db)

	rows, err := db.SearchAlerts(ctx, store.AlertQuery{Platform: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != "new" {
		t.Errorf("rows = %+v, want only what is still inside alert_keep_days", rows)
	}
}

func TestAnUndeclaredSignalKeepsOnlyTheDefaultRetention(t *testing.T) {
	c := &clock{t: base}
	db, err := store.Open(store.Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	now := state.MinuteOf(base)
	old := now - int64(signal.DefaultDurableDays+1)*24*60
	for _, minute := range []int64{old, now - 60} {
		_, err := db.DB().Exec(`INSERT INTO agg_total (platform, signal, minute, count, sum_ms, min_ms, max_ms, samples)
			VALUES ('test', 'retired', ?, 1, 1, 1, 1, 1)`, minute)
		if err != nil {
			t.Fatal(err)
		}
	}

	reg := signal.NewRegistry()
	brain := core.New(state.NewStore(reg, c.now), reg, slog.New(slog.NewTextHandler(discard{}, nil)), c.now)
	brain.Purge(context.Background(), db)

	var minutes []int64
	rows, err := db.DB().Query(`SELECT minute FROM agg_total WHERE signal = 'retired'`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var m int64
		if err := rows.Scan(&m); err != nil {
			t.Fatal(err)
		}
		minutes = append(minutes, m)
	}
	if len(minutes) != 1 || minutes[0] != now-60 {
		t.Errorf("minutes = %v, want only the recent one kept", minutes)
	}
}
