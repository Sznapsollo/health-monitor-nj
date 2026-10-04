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

func TestPacketsPerSenderSurviveARestart(t *testing.T) {
	c := &clock{t: base}
	log := slog.New(slog.NewTextHandler(discard{}, nil))
	db, err := store.Open(store.Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	var a state.Agg
	a.Add(12, 0, false)
	w := store.NewWriter(db, store.WriterOptions{FlushEvery: time.Millisecond, Log: log})
	w.Samples([]state.Sample{{
		Platform: "test", Signal: signal.PacketsSignal, Minute: state.MinuteOf(base) - 10,
		Dims: map[string]string{"sender": "web-1", "type": "request"}, Agg: a,
	}})
	w.Stop()

	reg := signal.NewRegistry()
	if err := reg.Add(&signal.Definition{Platform: "test", Name: "requests", Kind: signal.KindTimeseries}); err != nil {
		t.Fatal(err)
	}
	hot := state.NewStore(reg, c.now)
	brain := core.New(hot, reg, log, c.now)
	if err := brain.Restore(context.Background(), db); err != nil {
		t.Fatal(err)
	}

	view, ok := hot.View("test", signal.PacketsSignal, state.Criteria{HistoryMinutes: 60, GroupMinutes: 60, Group: "sender"})
	if !ok || len(view.Groups) != 1 || view.Groups[0].Value != "web-1" || view.Groups[0].Count != 12 {
		t.Fatalf("groups = %+v, want web-1's 12 packets from before the restart", view.Groups)
	}
}
