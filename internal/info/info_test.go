package info_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/info"
	"github.com/Sznapsollo/health-monitor-nj/internal/store"
)

func open(t *testing.T) *info.Store {
	t.Helper()
	db, err := store.Open(store.Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s, err := info.Open(context.Background(), db.DB())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestEachSenderKeepsItsNewestReports(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	at := time.Date(2026, 9, 26, 9, 0, 0, 0, time.UTC)
	for i := range 5 {
		if err := s.Record(ctx, "example", "jobsList", "config-1", map[string]any{"n": i}, at.Add(time.Duration(i)*time.Minute), 3); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Record(ctx, "example", "jobsList", "config-2", map[string]any{"n": 9}, at, 3); err != nil {
		t.Fatal(err)
	}

	entries, err := s.Entries(ctx, "example")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Key != "config-1" || entries[0].Versions != 3 || entries[1].Versions != 1 {
		t.Fatalf("entries = %+v, want config-1 with 3 versions and config-2 with 1", entries)
	}
	if !entries[0].Received.Equal(at.Add(4 * time.Minute)) {
		t.Fatalf("config-1 last received %v, want the fifth report", entries[0].Received)
	}

	_, latest, err := s.Content(ctx, "example", "jobsList", "config-1", 0)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]int
	_ = json.Unmarshal(latest, &got)
	if got["n"] != 4 {
		t.Fatalf("latest = %s, want n 4", latest)
	}

	versions, err := s.Versions(ctx, "example", "jobsList", "config-1")
	if err != nil {
		t.Fatal(err)
	}
	_, oldest, err := s.Content(ctx, "example", "jobsList", "config-1", versions[len(versions)-1].ID)
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(oldest, &got)
	if got["n"] != 2 {
		t.Fatalf("oldest kept = %s, want n 2", oldest)
	}
}

func TestReportsOlderThanTheirSignalKeepsThemGo(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 26, 9, 0, 0, 0, time.UTC)
	_ = s.Record(ctx, "example", "jobsList", "gone", map[string]any{}, now.AddDate(0, 0, -31), 20)
	_ = s.Record(ctx, "example", "jobsList", "here", map[string]any{}, now.AddDate(0, 0, -29), 20)

	stop, cancel := context.WithCancel(ctx)
	var purged int64
	s.PurgeEvery(stop, func(string, string) int { return 30 }, func() time.Time { return now }, time.Hour,
		func(n int64, err error) {
			if err != nil {
				t.Fatal(err)
			}
			purged = n
			cancel()
		})

	if purged != 1 {
		t.Fatalf("purged %d, want the one older than 30 days", purged)
	}
	if _, _, err := s.Content(ctx, "example", "jobsList", "gone", 0); !errors.Is(err, info.ErrNotFound) {
		t.Fatalf("the old sender is still kept: %v", err)
	}
	if _, _, err := s.Content(ctx, "example", "jobsList", "here", 0); err != nil {
		t.Fatalf("the recent sender went too: %v", err)
	}
}

func TestAMergedSignalKeepsOneRecordWithEverythingItWasTold(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	at := time.Date(2026, 9, 26, 9, 0, 0, 0, time.UTC)
	reports := []string{
		`{"type":"jobsList","jobsStatusMap":{"saveDocument":{"name":"saveDocument","heartBeat":1},"deleteBucket":{"name":"deleteBucket","heartBeat":1}}}`,
		`{"type":"jobsList","jobsStatusMap":{"saveDocument":{"name":"saveDocument","heartBeat":5,"config":{"host":"a"}}}}`,
		`{"type":"jobsList","jobsStatusMap":{"saveDocument":{"name":"saveDocument","heartBeat":9},"deleteBucket":{"name":"deleteBucket","heartBeat":9}}}`,
	}
	for i, r := range reports {
		var content map[string]any
		_ = json.Unmarshal([]byte(r), &content)
		if err := s.Merge(ctx, "example", "jobsList", "config-1", content, at.Add(time.Duration(i)*time.Minute), 20); err != nil {
			t.Fatal(err)
		}
	}

	versions, _ := s.Versions(ctx, "example", "jobsList", "config-1")
	if len(versions) != 1 || !versions[0].Received.Equal(at.Add(2*time.Minute)) {
		t.Fatalf("versions = %+v, want one record, as of the last report", versions)
	}
	_, content, _ := s.Content(ctx, "example", "jobsList", "config-1", 0)
	var got struct {
		Jobs map[string]struct {
			HeartBeat int
			Config    map[string]string
		} `json:"jobsStatusMap"`
	}
	_ = json.Unmarshal(content, &got)
	save := got.Jobs["saveDocument"]
	if save.HeartBeat != 9 || save.Config["host"] != "a" || got.Jobs["deleteBucket"].HeartBeat != 9 {
		t.Fatalf("merged = %s, want the latest heartbeats and the config a single-job report brought", content)
	}
}

func TestForgettingASenderLeavesTheOthers(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	at := time.Date(2026, 9, 26, 9, 0, 0, 0, time.UTC)
	for _, key := range []string{"test-1", "test-1", "config-1"} {
		if err := s.Record(ctx, "example", "jobsList", key, map[string]any{}, at, 20); err != nil {
			t.Fatal(err)
		}
	}
	n, err := s.Forget(ctx, "example", "jobsList", "test-1")
	if err != nil || n != 2 {
		t.Fatalf("forgot %d (%v), want both reports of test-1", n, err)
	}
	entries, _ := s.Entries(ctx, "example")
	if len(entries) != 1 || entries[0].Key != "config-1" {
		t.Fatalf("entries = %+v, want only config-1", entries)
	}
}
