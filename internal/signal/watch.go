package signal

import (
	"context"
	"log/slog"
	"path/filepath"

	"github.com/Sznapsollo/health-monitor-nj/internal/watchfs"
)

// Watch reloads platform catalogues when their files change, so adding a
// chart or changing a retention needs no restart.
func Watch(ctx context.Context, dir string, reg *Registry, log *slog.Logger) error {
	return watchfs.Run(ctx, watchfs.Options{
		Root:        dir,
		Log:         log,
		Interesting: func(path string) bool { return interesting(path) || watchfs.IsTopDir(dir, path) },
		OnChange: func(path string) {
			platform := platformOf(path)
			if watchfs.IsTopDir(dir, path) {
				platform = filepath.Base(path)
			}
			reload(dir, platform, reg, log)
		},
	})
}

func interesting(path string) bool {
	base := filepath.Base(path)
	if base == "signals.yaml" || base == "signals.yml" {
		return true
	}
	ext := filepath.Ext(base)
	return (ext == ".yaml" || ext == ".yml") && filepath.Base(filepath.Dir(path)) == DesignedDir
}

func platformOf(path string) string {
	dir := filepath.Dir(path)
	if filepath.Base(dir) == DesignedDir {
		dir = filepath.Dir(dir)
	}
	return filepath.Base(dir)
}

// Reload swaps a platform's signals in only when every file reads cleanly.
func Reload(dir, platform string, reg *Registry) (*Catalogue, error) {
	c, err := LoadPlatform(dir, platform)
	if err != nil {
		return nil, err
	}
	if err := reg.ReplacePlatform(c); err != nil {
		return nil, err
	}
	return c, nil
}

func reload(dir, platform string, reg *Registry, log *slog.Logger) {
	c, err := Reload(dir, platform, reg)
	if err != nil {
		log.Error("catalogue not reloaded", "platform", platform, "error", err)
		return
	}
	log.Info("catalogue reloaded", "platform", c.Platform, "signals", len(c.Signals))
}
