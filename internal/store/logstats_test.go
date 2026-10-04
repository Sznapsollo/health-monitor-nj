package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/store"
)

func settled(t *testing.T, s *store.Store, today string) map[string]store.LogDayStat {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		stats, err := s.LogDayStats(context.Background(), today)
		if err != nil {
			t.Fatal(err)
		}
		busy := false
		for _, d := range stats {
			busy = busy || d.Measuring
		}
		if !busy {
			return stats
		}
		if time.Now().After(deadline) {
			t.Fatalf("still measuring: %+v", stats)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestLogDaysAreMeasuredOnceAndCountedAsTheyGrow(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(store.Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	yesterday, today := base.Add(-24*time.Hour), base
	var rows []store.LogRow
	for i := range 30 {
		rows = append(rows, logRow(yesterday.Add(time.Duration(i)*time.Second), "dailyLogs", map[string]any{"url": "/api/work"}))
	}
	for i := range 10 {
		rows = append(rows, logRow(today.Add(time.Duration(i)*time.Second), "dailyLogs", map[string]any{"url": "/api/work"}))
	}
	if err := s.WriteLogs(ctx, rows); err != nil {
		t.Fatal(err)
	}
	day := today.Format("20060102")
	past := yesterday.Format("20060102")

	first, err := s.LogDayStats(ctx, day)
	if err != nil {
		t.Fatal(err)
	}
	if !first[past].Measuring {
		t.Fatalf("stats = %+v, want the unmeasured day reported as measuring", first)
	}

	stats := settled(t, s, day)
	if stats[past].Rows != 30 || stats[past].Bytes == 0 || stats[past].Estimated {
		t.Fatalf("yesterday = %+v, want 30 rows and its measured size", stats[past])
	}
	if stats[day].Rows != 10 || !stats[day].Estimated || stats[day].Bytes == 0 {
		t.Fatalf("today = %+v, want 10 rows and an estimated size", stats[day])
	}

	if err := s.WriteLogs(ctx, []store.LogRow{logRow(today.Add(time.Hour), "dailyLogs", map[string]any{})}); err != nil {
		t.Fatal(err)
	}
	if got := settled(t, s, day)[day].Rows; got != 11 {
		t.Fatalf("today = %d rows after one more, want 11", got)
	}
	_ = s.Close()

	again, err := store.Open(store.Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = again.Close() })
	reopened, err := again.LogDayStats(ctx, day)
	if err != nil {
		t.Fatal(err)
	}
	if reopened[past].Measuring || reopened[past].Rows != 30 {
		t.Fatalf("yesterday after a restart = %+v, want it kept, not measured again", reopened[past])
	}

	tables, _ := again.LogTables(ctx)
	for _, tb := range tables {
		if tb.Day == past {
			if err := again.DropLogTable(ctx, tb); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, ok := settled(t, again, day)[past]; ok {
		t.Fatal("a dropped day is still listed")
	}
	var saved int
	_ = again.DB().QueryRow(`SELECT COUNT(*) FROM log_table_stats`).Scan(&saved)
	if saved != 0 {
		t.Fatalf("log_table_stats has %d rows, want the dropped day's gone", saved)
	}
}
