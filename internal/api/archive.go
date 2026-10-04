package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/core"
	"github.com/Sznapsollo/health-monitor-nj/internal/store"
)

func sessionName(r *http.Request) string {
	if session, ok := SessionOf(r); ok && session.Name != "" {
		return session.Name
	}
	return "unknown"
}

func (d Deps) archiveDir() string {
	if d.Core == nil {
		return ""
	}
	return d.Core.ArchiveDir()
}

func (d Deps) logDay(w http.ResponseWriter, r *http.Request) (string, bool) {
	if d.DB == nil || d.Core == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "no log store"})
		return "", false
	}
	day := r.PathValue("day")
	if _, err := time.Parse("20060102", day); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "not a day"})
		return "", false
	}
	return day, true
}

func (d Deps) dayActionFailed(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, core.ErrToday):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error(), "reason": "today"})
	case errors.Is(err, core.ErrNoArchive):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error(), "reason": "noArchive"})
	case errors.Is(err, store.ErrArchiveMoved):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error(), "reason": "moved"})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
}

func (d Deps) handleArchiveDay(w http.ResponseWriter, r *http.Request) {
	day, ok := d.logDay(w, r)
	if !ok {
		return
	}
	tables, err := d.Core.ArchiveLogDay(context.WithoutCancel(r.Context()), d.DB, day)
	if err != nil {
		d.dayActionFailed(w, err)
		return
	}
	d.Log.Info("archived a day of logs by hand", "day", day, "by", sessionName(r))
	d.Core.ReclaimNow(d.DB)
	writeJSON(w, http.StatusOK, map[string]any{"status": "archived", "day": day, "tables": len(tables)})
}

func (d Deps) handleDeleteDay(w http.ResponseWriter, r *http.Request) {
	day, ok := d.logDay(w, r)
	if !ok {
		return
	}
	tables, err := d.Core.DropLogDay(context.WithoutCancel(r.Context()), d.DB, day)
	if err != nil {
		d.dayActionFailed(w, err)
		return
	}
	d.Log.Info("deleted a day of logs by hand", "day", day, "by", sessionName(r))
	d.Core.ReclaimNow(d.DB)
	writeJSON(w, http.StatusOK, map[string]any{"status": "deleted", "day": day, "tables": len(tables)})
}

func (d Deps) handleArchiveAlertDay(w http.ResponseWriter, r *http.Request) {
	day, ok := d.logDay(w, r)
	if !ok {
		return
	}
	f, err := d.Core.ArchiveAlertDay(context.WithoutCancel(r.Context()), d.DB, day)
	if err != nil {
		d.dayActionFailed(w, err)
		return
	}
	d.Log.Info("archived a day of alerts by hand", "day", day, "by", sessionName(r))
	d.Core.ReclaimNow(d.DB)
	writeJSON(w, http.StatusOK, map[string]any{"status": "archived", "day": day, "file": f.File})
}

func (d Deps) handleDeleteAlertDay(w http.ResponseWriter, r *http.Request) {
	day, ok := d.logDay(w, r)
	if !ok {
		return
	}
	n, err := d.Core.DropAlertDay(context.WithoutCancel(r.Context()), d.DB, day)
	if err != nil {
		d.dayActionFailed(w, err)
		return
	}
	d.Log.Info("deleted a day of alerts by hand", "day", day, "alerts", n, "by", sessionName(r))
	d.Core.ReclaimNow(d.DB)
	writeJSON(w, http.StatusOK, map[string]any{"status": "deleted", "day": day, "alerts": n})
}

func (d Deps) archivePath(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	dir := d.archiveDir()
	if dir == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": core.ErrNoArchive.Error()})
		return "", "", false
	}
	name := r.PathValue("file")
	path, ok := store.ArchivePath(dir, name)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "not an archive"})
		return "", "", false
	}
	return name, path, true
}

func (d Deps) handleArchiveFile(w http.ResponseWriter, r *http.Request) {
	name, path, ok := d.archivePath(w, r)
	if !ok {
		return
	}
	if info, err := os.Stat(path); err != nil || info.IsDir() {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such archive"})
		return
	}
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, name))
	http.ServeFile(w, r, path)
}

func (d Deps) handleDeleteArchive(w http.ResponseWriter, r *http.Request) {
	name, path, ok := d.archivePath(w, r)
	if !ok {
		return
	}
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such archive"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	d.Log.Info("log archive deleted", "file", name, "by", sessionName(r))
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted", "file": name})
}

func (d Deps) handleReclaim(w http.ResponseWriter, r *http.Request) {
	if d.DB == nil || d.Core == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "no database"})
		return
	}
	d.Core.ReclaimNow(d.DB)
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "reclaiming"})
}
