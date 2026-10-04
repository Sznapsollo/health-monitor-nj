package store_test

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/store"
)

func readArchive(t *testing.T, path string) []store.LogRow {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	var out []store.LogRow
	sc := bufio.NewScanner(zr)
	for sc.Scan() {
		var r store.LogRow
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatalf("line %q: %v", sc.Text(), err)
		}
		out = append(out, r)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func tableOf(t *testing.T, s *store.Store, kind string) store.LogTable {
	t.Helper()
	tables, err := s.LogTables(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, tb := range tables {
		if tb.Signal == kind {
			return tb
		}
	}
	t.Fatalf("no %s table in %v", kind, tables)
	return store.LogTable{}
}

func TestArchiveMovesADayToAGzipFile(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	dir := t.TempDir()
	if err := s.WriteLogs(ctx, []store.LogRow{
		logRow(base, "dailyLogs", map[string]any{"url": "/a"}),
		logRow(base.Add(time.Minute), "dailyLogs", map[string]any{"url": "/b"}),
	}); err != nil {
		t.Fatal(err)
	}

	f, err := s.ArchiveLogTable(ctx, tableOf(t, s, "dailyLogs"), dir)
	if err != nil {
		t.Fatal(err)
	}
	if f.File != "logs-20260919-dailyLogs.ndjson.gz" || f.Bytes <= 0 {
		t.Errorf("file = %+v", f)
	}
	rows := readArchive(t, filepath.Join(dir, f.File))
	if len(rows) != 2 || rows[0].Account != "acme" || rows[1].Payload["url"] != "/b" {
		t.Errorf("archived rows = %+v", rows)
	}
	if tables, _ := s.LogTables(ctx); len(tables) != 0 {
		t.Errorf("tables = %v, want the archived one dropped", tables)
	}

	if err := s.WriteLogs(ctx, []store.LogRow{logRow(base.Add(time.Hour), "dailyLogs", nil)}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ArchiveLogTable(ctx, tableOf(t, s, "dailyLogs"), dir); err != nil {
		t.Fatal(err)
	}
	if rows := readArchive(t, filepath.Join(dir, f.File)); len(rows) != 3 {
		t.Errorf("rows = %d, want a late row added to the earlier file", len(rows))
	}
	if info, err := os.Stat(filepath.Join(dir, f.File)); err != nil || info.Mode().Perm()&0o004 == 0 {
		t.Errorf("mode = %v, want an archive others can read, to copy it out", info.Mode())
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "*.partial")); len(left) != 0 {
		t.Errorf("left behind %v", left)
	}
}

func TestOnlyArchiveNamesReachTheDisk(t *testing.T) {
	for _, name := range []string{"hm.db", "../hm.db", "logs-2026091-x.ndjson.gz", "logs-20260919-a/b.ndjson.gz", "logs-20260919-x.ndjson.gz.partial"} {
		if _, ok := store.ArchivePath("/data/archive", name); ok {
			t.Errorf("%q accepted", name)
		}
	}
	if p, ok := store.ArchivePath("/data/archive", "logs-20260919-dailyLogs.ndjson.gz"); !ok || p != "/data/archive/logs-20260919-dailyLogs.ndjson.gz" {
		t.Errorf("path = %q, %v", p, ok)
	}
}

func TestArchivesAreDeletedDaysAfterTheyWereWritten(t *testing.T) {
	dir := t.TempDir()
	now := base
	for name, written := range map[string]time.Time{
		"logs-20260801-dailyLogs.ndjson.gz": now.AddDate(0, 0, -31),
		"logs-20260101-dailyLogs.ndjson.gz": now.AddDate(0, 0, -1),
		"alerts-20260901.ndjson.gz":         now.AddDate(0, 0, -11),
		"logs-20260801-sendLogs.ndjson.gz":  now.AddDate(0, 0, -400),
		"notes.txt":                         now.AddDate(0, 0, -400),
	} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, written, written); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := store.PurgeArchives(dir, now, func(f store.ArchiveFile) int {
		switch {
		case f.Alerts:
			return 10
		case f.Kind == "dailyLogs":
			return 30
		}
		return 0
	})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range removed {
		names = append(names, f.File)
	}
	sort.Strings(names)
	if want := []string{"alerts-20260901.ndjson.gz", "logs-20260801-dailyLogs.ndjson.gz"}; !reflect.DeepEqual(names, want) {
		t.Errorf("removed %v, want %v: a file counts from when it was written, not from its day", names, want)
	}
}

func TestADayOfAlertsIsArchivedAndLeavesTheDatabase(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	dir := t.TempDir()
	day := base.AddDate(0, 0, -12)
	if err := s.WriteAlerts(ctx, []store.AlertRow{
		{ID: "a1", Platform: "test", Level: "WARN", Message: "slow", Count: 3, First: day, Last: day},
		{ID: "a2", Platform: "other", Level: "ERROR", Message: "down", Count: 1, First: day.Add(time.Hour), Last: day.Add(time.Hour)},
		{ID: "a3", Platform: "test", Level: "WARN", Message: "today", Count: 1, First: base, Last: base},
	}); err != nil {
		t.Fatal(err)
	}
	f, err := s.ArchiveAlertDay(ctx, day.Format("20060102"), dir)
	if err != nil {
		t.Fatal(err)
	}
	if f.File != "alerts-"+day.Format("20060102")+".ndjson.gz" || !f.Alerts || f.Kind != "alerts" {
		t.Errorf("file = %+v", f)
	}
	zr := gzipReader(t, filepath.Join(dir, f.File))
	var got []store.AlertRow
	sc := bufio.NewScanner(zr)
	for sc.Scan() {
		var r store.AlertRow
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		got = append(got, r)
	}
	if len(got) != 2 || got[0].ID != "a1" || got[0].Count != 3 || got[1].Platform != "other" {
		t.Errorf("archived = %+v, want both platforms' alerts of that day", got)
	}
	days, err := s.AlertHistoryDays(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 1 || days[0] != base.Format("20060102") {
		t.Errorf("days left = %v, want only today's", days)
	}
	if _, ok := store.ArchivePath(dir, f.File); !ok {
		t.Error("an alert archive is not accepted as an archive name")
	}
}

func gzipReader(t *testing.T, path string) *gzip.Reader {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	return zr
}

func TestLogDaysAreMeasured(t *testing.T) {
	s := open(t)
	if err := s.WriteLogs(context.Background(), []store.LogRow{logRow(base, "dailyLogs", nil)}); err != nil {
		t.Fatal(err)
	}
	sizes, err := s.LogDayBytes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if sizes["20260919"] <= 0 || len(sizes) != 1 {
		t.Errorf("sizes = %v", sizes)
	}
}
