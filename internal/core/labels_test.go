package core_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/Sznapsollo/health-monitor-nj/internal/core"
	"github.com/Sznapsollo/health-monitor-nj/internal/signal"
	"github.com/Sznapsollo/health-monitor-nj/internal/state"
	"github.com/Sznapsollo/health-monitor-nj/internal/store"
)

func TestNamesSurviveARestart(t *testing.T) {
	c := &clock{t: base}
	db, err := store.Open(store.Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.DB().Exec(`INSERT INTO dim_label (platform, signal, dim, value, name, seen)
		VALUES ('test', 'requests', 'account', '42', 'Acme', ?), ('test', 'requests', 'url', '/a', 'Home', ?)`,
		state.MinuteOf(base), state.MinuteOf(base))
	if err != nil {
		t.Fatal(err)
	}

	reg := signal.NewRegistry()
	err = reg.Add(&signal.Definition{
		Platform: "test", Name: "requests", Kind: signal.KindTimeseries,
		Dims:      []string{"account", "accountName", "url"},
		Values:    signal.Values{Count: "count", MS: "ms"},
		Display:   signal.Display{Labels: map[string]string{"account": "accountName"}},
		Retention: signal.Retention{HotDetailMinutes: 5, HotTotalsMinutes: 120, DurableDays: 30},
	})
	if err != nil {
		t.Fatal(err)
	}
	hot := state.NewStore(reg, c.now)
	brain := core.New(hot, reg, slog.New(slog.NewTextHandler(discard{}, nil)), c.now)
	if err := brain.Restore(context.Background(), db); err != nil {
		t.Fatal(err)
	}

	if got := hot.Label("test", "requests", "account", "42"); got != "Acme" {
		t.Errorf("account 42 = %q, want its stored name", got)
	}
	if got := hot.Label("test", "requests", "url", "/a"); got != "" {
		t.Errorf("url /a = %q, want nothing for a dimension that is not labelled", got)
	}
}
