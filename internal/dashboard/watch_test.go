package dashboard_test

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/dashboard"
)

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

func TestWatchSeesADashboardMadeFromTheUI(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "acme"), 0o755); err != nil {
		t.Fatal(err)
	}
	store := dashboard.NewStore()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		if err := dashboard.Watch(ctx, root, store, slog.New(slog.NewTextHandler(discard{}, nil))); err != nil {
			t.Errorf("watch: %v", err)
		}
	}()

	made := dashboard.Dashboard{
		ID: "mine", Platform: "acme", Name: "Mine",
		Rows: []dashboard.Row{{Columns: []dashboard.Column{{Panels: []dashboard.Panel{{Type: dashboard.PanelStatus}}}}}},
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := dashboard.Save(root, made); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 8; i++ {
			time.Sleep(50 * time.Millisecond)
			if got := store.For("acme"); len(got) == 1 && got[0].ID == "mine" {
				return
			}
		}
	}
	t.Fatal("the watcher never saw the new file under dashboards/")
}

func TestWatchDropsDashboardsWhoseFolderIsMovedAway(t *testing.T) {
	root := t.TempDir()
	made := dashboard.Dashboard{
		ID: "mine", Platform: "old", Name: "Mine",
		Rows: []dashboard.Row{{Columns: []dashboard.Column{{Panels: []dashboard.Panel{{Type: dashboard.PanelStatus}}}}}},
	}
	if err := dashboard.Save(root, made); err != nil {
		t.Fatal(err)
	}
	store := dashboard.NewStore()
	loaded, err := dashboard.LoadPlatform(root, "old")
	if err != nil {
		t.Fatal(err)
	}
	store.Replace("old", loaded)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		if err := dashboard.Watch(ctx, root, store, slog.New(slog.NewTextHandler(discard{}, nil))); err != nil {
			t.Errorf("watch: %v", err)
		}
	}()
	time.Sleep(200 * time.Millisecond)

	if err := os.Rename(filepath.Join(root, "old"), filepath.Join(t.TempDir(), "old")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(store.For("old")) == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the moved platform's dashboards are still listed")
}
