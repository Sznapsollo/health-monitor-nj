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

func TestEachDayAndFileSaysWhenTheHourlyCheckTakesIt(t *testing.T) {
	started := time.Date(2026, 9, 26, 16, 54, 0, 0, time.UTC)
	c := &clock{t: started}
	reg := signal.NewRegistry()
	brain := core.New(state.NewStore(reg, c.now), reg, slog.New(slog.NewTextHandler(discard{}, nil)), c.now)
	brain.SetLogging(core.Logging{
		KeepDays: 1, KeepDaysByKind: map[string]int{"dailyLogs": 2},
		ArchiveDir: t.TempDir(), ArchiveKeepDays: 2, AlertDays: 2, AlertArchiveDays: 2,
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { brain.RolloverEvery(ctx, nil, time.Hour); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	deadline := time.Now().Add(time.Second)
	for brain.NextCheck(started.Add(time.Minute)).Equal(started.Add(time.Minute)) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}

	leaves := brain.LogDayLeaves("20260925", []string{"dailyLogs", "sendLogs"})
	want := map[string]time.Time{
		"dailyLogs": time.Date(2026, 9, 28, 0, 54, 0, 0, time.UTC),
		"sendLogs":  time.Date(2026, 9, 27, 0, 54, 0, 0, time.UTC),
	}
	for _, l := range leaves {
		if !l.On.Equal(want[l.Kind]) || !l.Archive {
			t.Errorf("%s leaves %v (archive %v), want %v as a file", l.Kind, l.On, l.Archive, want[l.Kind])
		}
	}

	if on, archive := brain.AlertDayLeaves("20260925"); !on.Equal(want["dailyLogs"]) || !archive {
		t.Errorf("alerts of the 25th leave %v (archive %v), want %v", on, archive, want["dailyLogs"])
	}

	written := time.Date(2026, 9, 25, 15, 26, 0, 0, time.UTC)
	on, ok := brain.FileDeleteOn(store.ArchiveFile{Kind: "dailyLogs", Written: written})
	if !ok || !on.Equal(time.Date(2026, 9, 27, 15, 54, 0, 0, time.UTC)) {
		t.Errorf("file deleted %v (%v), want the check after two days", on, ok)
	}
}
