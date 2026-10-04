// Package watchfs watches a directory tree for changes to the files that
// configure the server — signal catalogues and alert rules — so a change takes
// effect without a restart.
package watchfs

import (
	"context"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

// settle collapses the several events an editor produces when it saves a file
// into one reload.
const settle = 250 * time.Millisecond

// Options configure a watch.
type Options struct {
	// Root is the directory to watch, with every directory below it.
	Root string
	// Interesting decides which files are worth reacting to, by path.
	Interesting func(path string) bool
	// OnChange is called with the path of each changed file, after the events
	// have settled. It must not block for long.
	OnChange func(path string)
	Log      *slog.Logger
}

// Run watches until ctx is cancelled. A missing root is not an error: nothing
// is configured yet, and the caller carries on with its defaults.
func Run(ctx context.Context, o Options) error {
	if o.Log == nil {
		o.Log = slog.Default()
	}
	if _, err := os.Stat(o.Root); os.IsNotExist(err) {
		o.Log.Info("nothing to watch yet", "dir", o.Root)
		return nil
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer func() { _ = watcher.Close() }()

	watched := 0
	err = filepath.WalkDir(o.Root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		if err := watcher.Add(path); err != nil {
			o.Log.Warn("could not watch a directory", "dir", path, "error", err)
			return nil
		}
		watched++
		return nil
	})
	if err != nil {
		return err
	}
	o.Log.Info("watching for changes", "dir", o.Root, "directories", watched)

	var timer *time.Timer
	var pending []string
	fire := make(chan []string, 1)

	for {
		select {
		case <-ctx.Done():
			return nil

		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			var changed []string
			if event.Has(fsnotify.Create) {
				if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
					changed = addTree(watcher, event.Name, o)
				}
			}
			if o.Interesting(event.Name) {
				changed = append(changed, event.Name)
			}
			if len(changed) == 0 {
				continue
			}
			pending = append(pending, changed...)
			if timer != nil {
				timer.Stop()
			}
			paths := append([]string(nil), pending...)
			timer = time.AfterFunc(settle, func() {
				select {
				case fire <- paths:
				default:
				}
			})

		case paths := <-fire:
			pending = nil
			for _, path := range unique(paths) {
				o.OnChange(path)
			}

		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			o.Log.Warn("watcher", "dir", o.Root, "error", err)
		}
	}
}

// addTree reports files already inside: a tree made in one go is written before it is watched.
func addTree(watcher *fsnotify.Watcher, root string, o Options) []string {
	var found []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			if o.Interesting(path) {
				found = append(found, path)
			}
			return nil
		}
		if err := watcher.Add(path); err == nil {
			o.Log.Info("watching a new directory", "dir", path)
		}
		return nil
	})
	return found
}

func unique(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// IsTopDir reports a path naming a directory directly under root, such as a
// platform's folder; it may already be gone, as when the folder was moved away.
func IsTopDir(root, path string) bool {
	return filepath.Dir(filepath.Clean(path)) == filepath.Clean(root) && filepath.Ext(path) == ""
}
