package api

import (
	"encoding/json"
	"net/http"
	"sort"

	"github.com/Sznapsollo/health-monitor-nj/internal/core"
	"github.com/Sznapsollo/health-monitor-nj/internal/health"
	"github.com/Sznapsollo/health-monitor-nj/internal/sysstats"
)

// issues is every problem the monitor has with itself, quarantine first.
func (d Deps) issues() []health.Issue {
	var out []health.Issue
	if d.Core != nil {
		for _, g := range d.Core.QuarantineSnapshot() {
			first, last := g.First, g.Last
			out = append(out, health.Issue{
				Key: g.Key, Source: health.SourceQuarantine, Reason: g.Reason,
				Platform: g.Platform, Signal: g.Signal, Type: g.Type, Count: g.Count,
				First: &first, Last: &last, LastSource: g.LastSource,
			})
		}
	}
	if d.Gauges != nil {
		for _, platform := range d.Gauges.Platforms() {
			for _, name := range d.Gauges.Signals(platform) {
				if d.defined(platform, name) {
					continue
				}
				r := d.Gauges.ReportsOf(platform, name)
				first, last := r.First, r.Last
				out = append(out, health.Issue{
					Key:    "unknown_signal/" + platform + "/" + name + "/gauge",
					Source: health.SourceQuarantine, Reason: "unknown_signal",
					Platform: platform, Signal: name, Type: "gauge", Count: r.Count,
					First: &first, Last: &last, LastSource: r.LastSource,
				})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Last.After(*out[j].Last) })
	counter := func(name string, n int64) {
		if n > 0 {
			out = append(out, health.Issue{
				Key: health.CounterPrefix + name, Source: health.SourceCounter, Reason: name, Count: n,
			})
		}
	}
	if d.Intake != nil {
		c := d.Intake.Counters()
		counter("intake.kernelDrops", c.KernelDrops)
		counter("intake.tooOld", c.TooOld)
		counter("intake.tooNew", c.TooNew)
		counter("intake.chunksDropped", c.ChunksDropped)
		counter("intake.truncated", c.Truncated)
	}
	if d.Writer != nil {
		s := d.Writer.Stats()
		counter("writer.dropped", s.Dropped)
		counter("writer.errors", s.Errors)
	}
	if d.LogWriter != nil {
		s := d.LogWriter.Stats()
		counter("logWriter.sampled", s.Sampled)
		counter("logWriter.errors", s.Errors)
	}
	if d.AlertWriter != nil {
		s := d.AlertWriter.Stats()
		counter("alertWriter.dropped", s.Dropped)
		counter("alertWriter.errors", s.Errors)
	}
	if d.Statuses != nil {
		counter("statusWriter.dropped", d.Statuses.Stats().Dropped)
	}
	if d.Health != nil {
		out = d.Health.Annotate(out)
	}
	return out
}

// handleHealth lists the monitor's own problems and how many nobody has
// marked known yet.
func (d Deps) handleHealth(w http.ResponseWriter, _ *http.Request) {
	issues := d.issues()
	if issues == nil {
		issues = []health.Issue{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"issues": issues, "unknown": health.Unknown(issues)})
}

// handleMarkKnown marks issues known, or new again with "known": false.
func (d Deps) handleMarkKnown(w http.ResponseWriter, r *http.Request) {
	if d.Health == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "no database"})
		return
	}
	var body struct {
		Keys  []string `json:"keys"`
		Known *bool    `json:"known"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil || len(body.Keys) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "expected {\"keys\": [...]}"})
		return
	}
	who := "someone"
	if session, ok := SessionOf(r); ok && session.Name != "" {
		who = session.Name
	}
	if err := d.Health.Mark(d.issues(), body.Keys, body.Known == nil || *body.Known, who); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	d.handleHealth(w, r)
}

// handleServer reports what the monitor itself costs; history=1 adds the last
// hour of memory and CPU samples.
func (d Deps) handleServer(w http.ResponseWriter, r *http.Request) {
	if d.System == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "not sampled"})
		return
	}
	snap := d.System.Snapshot(r.URL.Query().Get("history") == "1")
	if d.DB != nil {
		snap.DBBytes = d.DB.Bytes()
	}
	var disks []core.DiskReading
	if d.Core != nil {
		disks = d.Core.DiskReadings()
	}
	writeJSON(w, http.StatusOK, struct {
		sysstats.Snapshot
		Disks []core.DiskReading `json:"disks,omitempty"`
	}{snap, disks})
}
