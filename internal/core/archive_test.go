package core_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/core"
	"github.com/Sznapsollo/health-monitor-nj/internal/signal"
	"github.com/Sznapsollo/health-monitor-nj/internal/state"
	"github.com/Sznapsollo/health-monitor-nj/internal/store"
)

func archiving(t *testing.T) (*core.Core, *store.Store, string) {
	t.Helper()
	c := &clock{t: base}
	db, err := store.Open(store.Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	reg := signal.NewRegistry()
	brain := core.New(state.NewStore(reg, c.now), reg, slog.New(slog.NewTextHandler(discard{}, nil)), c.now)
	dir := t.TempDir()
	brain.SetLogging(core.Logging{
		KeepDays:              14,
		KeepDaysByKind:        map[string]int{"dailyLogs": 2},
		ArchiveDir:            dir,
		ArchiveKeepDays:       30,
		ArchiveKeepDaysByKind: map[string]int{"auditLogs": 0},
	})
	return brain, db, dir
}

func TestOldDaysAreArchivedAndTheirFilesExpireAfterBeingWritten(t *testing.T) {
	brain, db, dir := archiving(t)
	threeDaysAgo, longAgo := base.AddDate(0, 0, -3), base.AddDate(0, 0, -40)
	logDay(t, db, threeDaysAgo, "dailyLogs")
	logDay(t, db, threeDaysAgo, "sendLogs")
	logDay(t, db, longAgo, "dailyLogs")
	logDay(t, db, longAgo, "auditLogs")

	brain.Rollover(context.Background(), db)

	tables, _ := db.LogTables(context.Background())
	if len(tables) != 1 || tables[0].Signal != "sendLogs" {
		t.Errorf("tables = %+v, want only sendLogs, still inside its 14 days", tables)
	}
	files, _ := store.Archives(dir)
	if len(files) != 2 {
		t.Fatalf("files = %+v, want both dailyLogs days archived and auditLogs dropped", files)
	}

	old := filepath.Join(dir, "logs-"+longAgo.Format("20060102")+"-dailyLogs.ndjson.gz")
	written := base.AddDate(0, 0, -31)
	if err := os.Chtimes(old, written, written); err != nil {
		t.Fatal(err)
	}
	brain.Rollover(context.Background(), db)
	files, _ = store.Archives(dir)
	if len(files) != 1 || files[0].Day != threeDaysAgo.Format("20060102") {
		t.Errorf("files = %+v, want the one written 31 days ago gone", files)
	}
}

func TestOldAlertsAreArchived(t *testing.T) {
	brain, db, dir := archiving(t)
	brain.SetLogging(core.Logging{KeepDays: 14, ArchiveDir: dir, AlertDays: 10, AlertArchiveDays: 10})
	old, recent := base.AddDate(0, 0, -11), base.AddDate(0, 0, -2)
	if err := db.WriteAlerts(context.Background(), []store.AlertRow{
		{ID: "old", Platform: "test", Level: "WARN", Message: "x", Count: 1, First: old, Last: old},
		{ID: "recent", Platform: "test", Level: "WARN", Message: "y", Count: 1, First: recent, Last: recent},
	}); err != nil {
		t.Fatal(err)
	}
	brain.Rollover(context.Background(), db)
	days, _ := db.AlertHistoryDays(context.Background())
	if len(days) != 1 || days[0] != recent.Format("20060102") {
		t.Errorf("alert days = %v, want only the recent one left", days)
	}
	files, _ := store.Archives(dir)
	if len(files) != 1 || !files[0].Alerts || files[0].Day != old.Format("20060102") {
		t.Errorf("files = %+v, want the old day as an alert archive", files)
	}
	if brain.AlertRetention() != (core.Retention{Kind: "alerts", DBDays: 10, ArchiveDays: 10}) {
		t.Errorf("alert retention = %+v", brain.AlertRetention())
	}
}

func TestTodayCannotBeArchivedOrDeleted(t *testing.T) {
	brain, db, _ := archiving(t)
	logDay(t, db, base, "dailyLogs")
	today := base.Format("20060102")
	if _, err := brain.ArchiveLogDay(context.Background(), db, today); !errors.Is(err, core.ErrToday) {
		t.Errorf("archive today: %v", err)
	}
	if _, err := brain.DropLogDay(context.Background(), db, today); !errors.Is(err, core.ErrToday) {
		t.Errorf("delete today: %v", err)
	}
	if tables, _ := db.LogTables(context.Background()); len(tables) != 1 {
		t.Errorf("tables = %+v, want today kept", tables)
	}
}

func TestActivitySurvivesItsLogsLeaving(t *testing.T) {
	brain, db, _ := archiving(t)
	ctx := context.Background()
	archived, deleted, aged := base.AddDate(0, 0, -1), base.AddDate(0, 0, -2), base.AddDate(0, 0, -3)
	for _, at := range []time.Time{archived, deleted, aged} {
		logDay(t, db, at, "dailyLogs")
	}
	if _, err := brain.ArchiveLogDay(ctx, db, archived.Format("20060102")); err != nil {
		t.Fatal(err)
	}
	if _, err := brain.DropLogDay(ctx, db, deleted.Format("20060102")); err != nil {
		t.Fatal(err)
	}
	brain.Rollover(ctx, db)
	for _, at := range []time.Time{archived, deleted, aged} {
		day := at.Format("20060102")
		if _, err := db.ComputeActivity(ctx, day); err != nil {
			t.Fatal(err)
		}
		rows, err := db.ActivityFor(ctx, day)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 || rows[0].User != "anna" {
			t.Errorf("%s activity = %+v, want anna kept after the logs left", day, rows)
		}
	}
}

func TestRetentionIsReportedPerKind(t *testing.T) {
	brain, _, _ := archiving(t)
	fallback, kinds := brain.LogRetention([]string{"sendLogs", "dailyLogs"})
	if fallback != (core.Retention{DBDays: 14, ArchiveDays: 30}) {
		t.Errorf("default = %+v", fallback)
	}
	want := []core.Retention{
		{Kind: "auditLogs", DBDays: 14},
		{Kind: "dailyLogs", DBDays: 2, ArchiveDays: 30},
		{Kind: "sendLogs", DBDays: 14, ArchiveDays: 30},
	}
	if len(kinds) != len(want) {
		t.Fatalf("kinds = %+v", kinds)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Errorf("kinds[%d] = %+v, want %+v", i, kinds[i], want[i])
		}
	}
}
