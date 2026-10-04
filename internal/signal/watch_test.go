package signal_test

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/signal"
)

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

// writeUntil keeps rewriting the file until the watcher is seen to act on it,
// so the test does not depend on winning a race with the watch registering.
func writeUntil(t *testing.T, write func(string), body string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		write(body)
		for i := 0; i < 8; i++ {
			time.Sleep(50 * time.Millisecond)
			if cond() {
				return
			}
		}
	}
	t.Fatal("the watcher never picked the change up")
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestWatchReloadsOnChange(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "acme")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "signals.yaml")
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("signals:\n  requests:\n    kind: timeseries\n    input:\n      dims: [url]\n")

	reg := signal.NewRegistry()
	c, err := signal.LoadCatalogue(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.AddCatalogue(c); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		if err := signal.Watch(ctx, root, reg, slog.New(slog.NewTextHandler(discard{}, nil))); err != nil {
			t.Errorf("watch: %v", err)
		}
	}()

	// A new signal appears without a restart. The write is repeated until the
	// watch is seen to react: the goroutine above may not have registered yet
	// when the first one lands, and a missed inotify event never comes back.
	updated := "signals:\n  requests:\n    kind: timeseries\n    input:\n      dims: [url, user]\n" +
		"  jobs:\n    kind: timeseries\n    input:\n      dims: [jobName]\n"
	writeUntil(t, write, updated, func() bool {
		_, ok := reg.Lookup("acme", "jobs")
		return ok
	})
	waitFor(t, "the changed dimensions", func() bool {
		d, ok := reg.Lookup("acme", "requests")
		return ok && len(d.Dims) == 2
	})

	// A broken file must not take the working definitions away.
	write("signals:\n  requests:\n    kind: nonsense\n")
	time.Sleep(700 * time.Millisecond)
	if d, ok := reg.Lookup("acme", "requests"); !ok || len(d.Dims) != 2 {
		t.Fatalf("a bad catalogue replaced the good one: %+v", d)
	}
}

func TestWatchPicksUpANewPlatform(t *testing.T) {
	root := t.TempDir()
	reg := signal.NewRegistry()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		if err := signal.Watch(ctx, root, reg, slog.New(slog.NewTextHandler(discard{}, nil))); err != nil {
			t.Errorf("watch: %v", err)
		}
	}()
	// Give the watcher a moment to register before the directory appears.
	time.Sleep(100 * time.Millisecond)

	dir := filepath.Join(root, "newplatform")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	body := "signals:\n  orders:\n    kind: timeseries\n    input:\n      dims: [status]\n"
	writeUntil(t, func(s string) {
		if err := os.WriteFile(filepath.Join(dir, "signals.yaml"), []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}, body, func() bool {
		_, ok := reg.Lookup("newplatform", "orders")
		return ok
	})
}

func TestWatchPicksUpADesignedFile(t *testing.T) {
	root := t.TempDir()
	reg := signal.NewRegistry()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		if err := signal.Watch(ctx, root, reg, slog.New(slog.NewTextHandler(discard{}, nil))); err != nil {
			t.Errorf("watch: %v", err)
		}
	}()
	time.Sleep(100 * time.Millisecond)

	folder := filepath.Join(root, "demo", signal.DesignedDir)
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	writeUntil(t, func(s string) {
		if err := os.WriteFile(filepath.Join(folder, "checkout.yaml"), []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}, "kind: timeseries\ninput:\n  dims: [region]\n", func() bool {
		_, ok := reg.Lookup("demo", "checkout")
		return ok
	})
}

func TestWatchDropsAPlatformWhoseFolderIsMovedAway(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "old")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "signals:\n  orders:\n    kind: timeseries\n    input:\n      dims: [status]\n"
	if err := os.WriteFile(filepath.Join(dir, "signals.yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	reg := signal.NewRegistry()
	if _, err := signal.Reload(root, "old", reg); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		if err := signal.Watch(ctx, root, reg, slog.New(slog.NewTextHandler(discard{}, nil))); err != nil {
			t.Errorf("watch: %v", err)
		}
	}()
	time.Sleep(200 * time.Millisecond)

	if err := os.Rename(dir, filepath.Join(t.TempDir(), "old")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the moved platform to leave the registry", func() bool {
		_, ok := reg.Lookup("old", "orders")
		return !ok
	})
}
