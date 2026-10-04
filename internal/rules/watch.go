package rules

import (
	"context"
	"log/slog"
	"path/filepath"

	"github.com/Sznapsollo/health-monitor-nj/internal/watchfs"
)

// Watch reloads alert rules when their files change, which is how the
// Settings panel's save takes effect within a second.
func Watch(ctx context.Context, dir string, apply func(*Set), log *slog.Logger) error {
	return watchfs.Run(ctx, watchfs.Options{
		Root: dir,
		Log:  log,
		Interesting: func(path string) bool {
			base := filepath.Base(path)
			return base == "rules.yaml" || base == "rules.yml"
		},
		OnChange: func(path string) {
			s, err := Load(path)
			if err != nil {
				// Bad rules keep the old ones in force rather than silencing
				// or flooding everything.
				log.Error("rules not reloaded", "file", path, "error", err)
				return
			}
			apply(s)
			log.Info("rules reloaded", "platform", s.Platform, "overrides", len(s.Latency.Overrides), "file", path)
		},
	})
}
