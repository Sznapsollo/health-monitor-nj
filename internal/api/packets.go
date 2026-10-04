package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/intake"
)

// handlePackets shows the datagrams received since seq, and keeps the tap on
// for as long as someone keeps asking.
func (d Deps) handlePackets(w http.ResponseWriter, r *http.Request) {
	if d.Tap == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "packets are not shown here"})
		return
	}
	since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	d.Tap.Watch(time.Now())
	entries, seq := d.Tap.Since(since)
	writeJSON(w, http.StatusOK, map[string]any{
		"entries": entries, "seq": seq,
		"keep": intake.TapSize, "rawBytes": intake.TapRaw, "windowSeconds": int(intake.TapWindow.Seconds()),
	})
}
