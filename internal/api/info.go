package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/info"
	"github.com/Sznapsollo/health-monitor-nj/internal/signal"
)

type infoEntry struct {
	info.Entry
	DisplayName string `json:"displayName"`
	PacketType  string `json:"packetType,omitempty"`
	Merge       bool   `json:"merge,omitempty"`
	NoStatus    bool   `json:"noStatus,omitempty"`
	Defined     bool   `json:"defined"`
	Offline     bool   `json:"offline,omitempty"`
}

func (d Deps) handleInfo(w http.ResponseWriter, r *http.Request) {
	platform := d.platformOf(r)
	out := []infoEntry{}
	if d.Info == nil {
		writeJSON(w, http.StatusOK, map[string]any{"platform": platform, "entries": out})
		return
	}
	entries, err := d.Info.Entries(r.Context(), platform)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	offline := map[string]bool{}
	if d.Statuses != nil {
		for _, e := range d.Statuses.List(platform) {
			offline[e.Signal+"\x00"+e.Key] = e.Offline
		}
	}
	for _, e := range entries {
		row := infoEntry{Entry: e, DisplayName: e.Signal, Offline: offline[e.Signal+"\x00"+e.Key]}
		if def, ok := d.lookup(platform, e.Signal); ok && def.Kind == signal.KindInfo {
			row.DisplayName, row.PacketType, row.Merge, row.Defined = def.Display.Name, def.PacketType, def.Merge, true
			row.NoStatus = def.NoStatus
		}
		out = append(out, row)
	}
	writeJSON(w, http.StatusOK, map[string]any{"platform": platform, "entries": out})
}

func (d Deps) handleInfoReport(w http.ResponseWriter, r *http.Request) {
	if d.Info == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no reports are kept"})
		return
	}
	platform := d.platformOf(r)
	q := r.URL.Query()
	name, key := q.Get("signal"), q.Get("key")
	id, _ := strconv.ParseInt(q.Get("id"), 10, 64)
	version, content, err := d.Info.Content(r.Context(), platform, name, key, id)
	if errors.Is(err, info.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	versions, err := d.Info.Versions(r.Context(), platform, name, key)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, struct {
		ID       int64           `json:"id"`
		Received time.Time       `json:"received"`
		Content  json.RawMessage `json:"content"`
		Versions []info.Version  `json:"versions"`
	}{version.ID, version.Received, content, versions})
}

// handleForgetInfo deletes a sender's kept reports of an info signal, and its
// row on the Status tab, so test data or a sender gone for good leaves no trace.
func (d Deps) handleForgetInfo(w http.ResponseWriter, r *http.Request) {
	if d.Info == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no reports are kept"})
		return
	}
	platform := d.platformOf(r)
	q := r.URL.Query()
	name, key := q.Get("signal"), q.Get("key")
	if name == "" || key == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "signal and key are required"})
		return
	}
	n, err := d.Info.Forget(r.Context(), platform, name, key)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if d.Statuses != nil {
		if err := d.Statuses.Forget(r.Context(), platform, name, key); err != nil {
			d.Log.Warn("could not forget the status of an info sender", "signal", name, "key", key, "error", err)
		}
	}
	d.Log.Info("info reports removed", "platform", platform, "signal", name, "key", key, "reports", n, "by", sessionName(r))
	writeJSON(w, http.StatusOK, map[string]any{"status": "removed", "reports": n})
}

func (d Deps) lookup(platform, name string) (*signal.Definition, bool) {
	if d.Registry == nil {
		return nil, false
	}
	return d.Registry.Lookup(platform, name)
}
