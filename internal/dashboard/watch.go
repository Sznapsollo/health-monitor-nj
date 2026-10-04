package dashboard

import (
	"context"
	"log/slog"
	"path/filepath"

	"github.com/Sznapsollo/health-monitor-nj/internal/watchfs"
)

// Watch reloads dashboards when their files change, so rearranging a screen
// takes effect within a second and needs no restart.
func Watch(ctx context.Context, dir string, store *Store, log *slog.Logger) error {
	return watchfs.Run(ctx, watchfs.Options{
		Root:        dir,
		Log:         log,
		Interesting: func(path string) bool { return interesting(path) || watchfs.IsTopDir(dir, path) },
		OnChange: func(path string) {
			platform := platformOf(path)
			if watchfs.IsTopDir(dir, path) {
				platform = filepath.Base(path)
			}
			dashboards, err := LoadPlatform(dir, platform)
			if err != nil {
				log.Error("dashboards not reloaded", "file", path, "error", err)
				return
			}
			store.Replace(platform, dashboards)
			log.Info("dashboards reloaded", "platform", platform, "count", len(dashboards), "file", path)
		},
	})
}

func interesting(path string) bool {
	base := filepath.Base(path)
	if base == "dashboards.yaml" || base == "dashboards.yml" {
		return true
	}
	ext := filepath.Ext(base)
	return (ext == ".yaml" || ext == ".yml") && filepath.Base(filepath.Dir(path)) == SharedDir
}

// platformOf is the platform directory a dashboard file belongs to, whether
// it is the shipped file or one under dashboards/.
func platformOf(path string) string {
	dir := filepath.Dir(path)
	if filepath.Base(dir) == SharedDir {
		dir = filepath.Dir(dir)
	}
	return filepath.Base(dir)
}
