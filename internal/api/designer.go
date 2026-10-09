package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/adapter/legacy"
	"github.com/Sznapsollo/health-monitor-nj/internal/core"
	"github.com/Sznapsollo/health-monitor-nj/internal/dashboard"
	"github.com/Sznapsollo/health-monitor-nj/internal/gauge"
	"github.com/Sznapsollo/health-monitor-nj/internal/signal"
	"github.com/Sznapsollo/health-monitor-nj/internal/store"
)

// signalEdit carries no views: the standard ones are built from kind, values and groupTop.
type signalEdit struct {
	Kind        string            `json:"kind"`
	DisplayName string            `json:"displayName"`
	Dims        []dimInfo         `json:"dims"`
	Values      map[string]string `json:"values"`
	Retention   retentionInfo     `json:"retention"`
	MaxKeys     int               `json:"maxKeys"`
	TTLSeconds  int               `json:"ttlSeconds"`
	GroupTop    int               `json:"groupTop"`
	KeepPairs   [][2]string       `json:"keepPairs"`
	PacketType  string            `json:"packetType"`
	Merge       bool              `json:"merge"`
	NoStatus    bool              `json:"noStatus"`
	Colors      map[string]string `json:"colors"`
}

func (d Deps) handleSignalCandidates(w http.ResponseWriter, r *http.Request) {
	out := []core.Candidate{}
	if d.Core != nil {
		for _, c := range d.Core.Candidates() {
			if c.Kind == string(signal.KindInfo) && d.Registry != nil &&
				(d.Registry.InfoFor(c.Platform, c.Signal) != "" || d.Registry.MetricFor(c.Platform, c.Signal) != "") {
				continue
			}
			if !d.defined(c.Platform, c.Signal) {
				out = append(out, c)
			}
		}
	}
	if d.Gauges != nil {
		for _, platform := range d.Gauges.Platforms() {
			for _, name := range d.Gauges.Signals(platform) {
				if d.defined(platform, name) {
					continue
				}
				out = append(out, gaugeCandidate(d.Gauges.View(platform, name)))
			}
		}
	}
	out = append(out, d.logCandidates(r)...)
	if only := r.URL.Query().Get("platform"); only != "" {
		out = slices.DeleteFunc(out, func(c core.Candidate) bool { return c.Platform != only })
	}
	writeJSON(w, http.StatusOK, map[string]any{"candidates": out})
}

// logCandidates are the kinds of log that have arrived with no signal of
// their own. Log days are shared by every platform, so each is offered for
// the platform asked about.
// handleDismissCandidate removes a signal that arrived undefined, when it was
// sent by mistake: a chart's gathered sample, a gauge's readings, or every
// stored day of a log. It is listed again if it arrives again.
func (d Deps) handleDismissCandidate(w http.ResponseWriter, r *http.Request) {
	name, platform, kind := r.PathValue("name"), r.URL.Query().Get("platform"), r.URL.Query().Get("kind")
	if name == "" || platform == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name and platform are required"})
		return
	}
	if d.defined(platform, name) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": name + " is defined; delete it on the Signals tab instead"})
		return
	}
	switch signal.Kind(kind) {
	case signal.KindLog:
		if builtInLogs[name] || d.Core == nil || d.DB == nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": name + " cannot be removed here"})
			return
		}
		tables, err := d.Core.DropLogKind(context.WithoutCancel(r.Context()), d.DB, name)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		d.Core.ReclaimNow(d.DB)
		d.Log.Info("removed an undefined log", "platform", platform, "kind", name, "days", len(tables), "by", sessionName(r))
	case signal.KindGauge:
		if d.Gauges != nil {
			d.Gauges.Forget(platform, name)
		}
		d.Log.Info("removed an undefined gauge", "platform", platform, "signal", name, "by", sessionName(r))
	default:
		if d.Core != nil {
			d.Core.DismissCandidate(platform, name)
		}
		d.Log.Info("removed an undefined signal", "platform", platform, "signal", name, "by", sessionName(r))
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "removed", "signal": name})
}

// handleCandidateSample shows what an undefined signal arrived with: the last
// packet of a chart or a report type, a gauge's current readings, or a log's
// newest row.
func (d Deps) handleCandidateSample(w http.ResponseWriter, r *http.Request) {
	name, platform, kind := r.PathValue("name"), r.URL.Query().Get("platform"), r.URL.Query().Get("kind")
	var sample any
	switch signal.Kind(kind) {
	case signal.KindLog:
		if d.DB != nil {
			rows, err := d.DB.SearchLogs(r.Context(), store.LogQuery{
				Platform: platform, Signal: name, From: time.Unix(0, 0), Limit: 1,
			})
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			if len(rows) > 0 {
				sample = rows[0]
			}
		}
	case signal.KindGauge:
		if d.Gauges != nil {
			if v := d.Gauges.View(platform, name); len(v.Points) > 0 {
				sample = v
			}
		}
	default:
		if d.Core != nil {
			if raw, ok := d.Core.CandidateSample(platform, name); ok {
				sample = raw
			}
		}
	}
	if sample == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "nothing of " + name + " is kept to show"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sample": sample})
}

// handleLogRetention is what config.yaml gives a kind of log, which the
// signal editor shows as what an empty field means.
func (d Deps) handleLogRetention(w http.ResponseWriter, r *http.Request) {
	if d.Core == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "no log retention"})
		return
	}
	writeJSON(w, http.StatusOK, d.Core.ConfiguredRetention(r.URL.Query().Get("kind")))
}

// builtInLogs are the monitor's own kinds of log, kept as config.yaml says
// with nothing to define.
var builtInLogs = map[string]bool{legacy.SignalDailyLogs: true, legacy.SignalSendLogs: true}

func (d Deps) logCandidates(r *http.Request) []core.Candidate {
	if d.DB == nil {
		return nil
	}
	tables, err := d.DB.LogTables(r.Context())
	if err != nil {
		return nil
	}
	platform := d.platformOf(r)
	var out []core.Candidate
	seen := map[string]bool{}
	for i := len(tables) - 1; i >= 0; i-- {
		t := tables[i]
		if t.Signal == "" || seen[t.Signal] || builtInLogs[t.Signal] || d.defined(platform, t.Signal) {
			continue
		}
		seen[t.Signal] = true
		day, _ := time.Parse("20060102", t.Day)
		out = append(out, core.Candidate{
			Platform: platform, Signal: t.Signal, Kind: string(signal.KindLog),
			First: day, Last: day,
			Dims: map[string][]string{}, Values: map[string]core.Range{}, Minutes: []core.CandidateMinute{},
		})
	}
	return out
}

func gaugeCandidate(v gauge.View) core.Candidate {
	c := core.Candidate{
		Platform: v.Platform, Signal: v.Signal, Kind: string(signal.KindGauge),
		First: v.UpdatedAt, Last: v.UpdatedAt,
		Dims: map[string][]string{"label": {}}, Values: map[string]core.Range{},
		Minutes: []core.CandidateMinute{},
	}
	for i, p := range v.Points {
		if i < 10 {
			c.Dims["label"] = append(c.Dims["label"], p.Label)
		}
		r, ok := c.Values["value"]
		if !ok {
			r = core.Range{Min: p.Value, Max: p.Value}
		}
		r.Min, r.Max = min(r.Min, p.Value), max(r.Max, p.Value)
		c.Values["value"] = r
	}
	return c
}

func (d Deps) defined(platform, name string) bool {
	if d.Registry == nil {
		return false
	}
	def, ok := d.Registry.Lookup(platform, name)
	return ok && !def.AutoRegistered
}

func (d Deps) handlePutSignal(w http.ResponseWriter, r *http.Request) {
	if d.Registry == nil || d.PlatformsDir == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "signals cannot be saved here"})
		return
	}
	name := r.PathValue("name")
	platform := r.URL.Query().Get("platform")
	if err := d.designablePlatform(platform); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := signal.ValidName(name); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	var body signalEdit
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "could not read that signal"})
		return
	}
	kind := signal.Kind(body.Kind)
	if kind != signal.KindTimeseries && kind != signal.KindGauge && kind != signal.KindLog && kind != signal.KindInfo {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "the designer makes timeseries, gauge, log and info signals; status and alert need no definition",
		})
		return
	}

	who := "someone"
	if session, ok := SessionOf(r); ok && session.Name != "" {
		who = session.Name
	}
	def := signal.Definition{
		Platform: platform,
		Name:     name,
		Kind:     kind,
		Values:   signal.Values{Count: body.Values["count"], MS: body.Values["ms"]},
		Retention: signal.Retention{
			HotDetailMinutes: body.Retention.HotDetailMinutes,
			HotTotalsMinutes: body.Retention.HotTotalsMinutes,
			DurableDays:      body.Retention.DurableDays,
			DetailDays:       body.Retention.DetailDays,
			LogDays:          body.Retention.LogDays,
			ArchiveDays:      body.Retention.ArchiveDays,
			Versions:         body.Retention.Versions,
		},
		PacketType: strings.TrimSpace(body.PacketType),
		Merge:      body.Merge,
		NoStatus:   body.NoStatus && kind == signal.KindInfo,
		KeepPairs:  body.KeepPairs,
		MaxKeys:    body.MaxKeys,
		TTLSeconds: body.TTLSeconds,
		Display:    signal.Display{Name: body.DisplayName, Dims: map[string]string{}},
		CreatedBy:  who,
		UpdatedBy:  who,
	}
	if kind == signal.KindGauge {
		def.Values = signal.Values{}
	}
	for _, dim := range body.Dims {
		def.Dims = append(def.Dims, dim.Name)
		if dim.DisplayName != "" && dim.DisplayName != dim.Name {
			def.Display.Dims[dim.Name] = dim.DisplayName
		}
		if dim.LabelDim != "" {
			if def.Display.Labels == nil {
				def.Display.Labels = map[string]string{}
			}
			def.Display.Labels[dim.Name] = dim.LabelDim
		}
	}
	if kind == signal.KindLog || kind == signal.KindInfo {
		def.Values, def.Dims, def.Display.Labels, def.KeepPairs = signal.Values{}, nil, nil, nil
	}
	if kind == signal.KindLog || kind == signal.KindGauge {
		def.PacketType = ""
	}
	if kind == signal.KindTimeseries && len(body.Colors) > 0 {
		def.Display.Colors = body.Colors
	}
	if kind == signal.KindTimeseries || kind == signal.KindGauge {
		def.Views = standardViews(kind, def.Values.MS != "", body.GroupTop)
	}

	if have, ok := d.Registry.Lookup(platform, name); ok && !have.AutoRegistered {
		if have.ReadOnly {
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": name + " comes from signals.yaml and is edited there",
			})
			return
		}
		if have.CreatedBy != "" {
			def.CreatedBy = have.CreatedBy
		}
	}
	if err := signal.Save(d.PlatformsDir, def); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if _, err := signal.Reload(d.PlatformsDir, platform, d.Registry); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if def.NoStatus && d.Statuses != nil {
		if err := d.Statuses.ForgetSignal(r.Context(), platform, name); err != nil {
			d.Log.Warn("could not take an info signal off the Status tab", "signal", name, "error", err)
		}
	}
	if d.Core != nil {
		d.Core.ForgetCandidate(platform, name)
		if def.PacketType != "" {
			d.Core.ForgetCandidate(platform, def.PacketType)
		}
	}
	d.Log.Info("signal saved", "platform", platform, "signal", name, "by", who)
	saved, _ := d.Registry.Lookup(platform, name)
	writeJSON(w, http.StatusOK, infoOf(saved))
}

func (d Deps) handleDeleteSignal(w http.ResponseWriter, r *http.Request) {
	if d.Registry == nil || d.PlatformsDir == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "signals cannot be deleted here"})
		return
	}
	name := r.PathValue("name")
	platform := r.URL.Query().Get("platform")
	if err := signal.ValidName(platform); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := signal.ValidName(name); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if have, ok := d.Registry.Lookup(platform, name); ok && have.ReadOnly {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": name + " comes from signals.yaml and can only be removed there",
		})
		return
	}
	if d.Dashboards != nil {
		for _, board := range d.Dashboards.For(platform) {
			if usesSignal(board, name) {
				writeJSON(w, http.StatusConflict, map[string]string{
					"error": "the dashboard " + board.Name + " charts " + name + "; take it off there first",
				})
				return
			}
		}
	}
	if err := signal.Delete(d.PlatformsDir, platform, name); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if _, err := signal.Reload(d.PlatformsDir, platform, d.Registry); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	d.Log.Info("signal deleted", "platform", platform, "signal", name)
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted", "signal": name})
}

func usesSignal(board dashboard.Dashboard, name string) bool {
	for _, row := range board.Rows {
		for _, column := range row.Columns {
			for _, panel := range column.Panels {
				if panel.Signal == name {
					return true
				}
			}
		}
	}
	return false
}

// designablePlatform accepts a known platform, one with packets waiting, or one with a directory.
func (d Deps) designablePlatform(platform string) error {
	if err := signal.ValidName(platform); err != nil {
		return err
	}
	if d.knownPlatform(platform) {
		return nil
	}
	if d.Core != nil {
		for _, c := range d.Core.Candidates() {
			if c.Platform == platform {
				return nil
			}
		}
	}
	if d.Gauges != nil && slices.Contains(d.Gauges.Platforms(), platform) {
		return nil
	}
	if info, err := os.Stat(filepath.Join(d.PlatformsDir, platform)); err == nil && info.IsDir() {
		return nil
	}
	return fmt.Errorf("nothing is known about platform %s: no signals, no packets waiting and no directory", platform)
}

func standardViews(kind signal.Kind, hasMS bool, top int) []signal.View {
	if kind == signal.KindGauge {
		return []signal.View{{ID: "bars", Type: "barGauge", DefaultOn: true}}
	}
	if top <= 0 {
		top = 50
	}
	var options map[string]any
	if hasMS {
		options = map[string]any{"secondaryValue": "avgMs", "secondaryStyle": "line"}
	}
	return []signal.View{
		{ID: "all", Type: "minuteSeries", Value: "count", Style: "column", DefaultOn: true, Options: options},
		{ID: "byGroup", Type: "minuteSeriesPerGroup", Value: "count", Style: "column", Top: top, DefaultOn: true, Options: options},
	}
}

// handleExportSignals downloads a platform's signals as a signals.yaml.
func (d Deps) handleExportSignals(w http.ResponseWriter, r *http.Request) {
	platform := r.URL.Query().Get("platform")
	if d.Registry == nil || !d.knownPlatform(platform) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no platform " + platform})
		return
	}
	b, err := signal.Export(platform, d.Registry.Definitions(platform))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/yaml")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="signals_%s.yaml"`, platform))
	_, _ = w.Write(b)
}

// handleImportSignals defines the signals of an exported file that the
// platform does not define yet; the ones it does are left as they are.
func (d Deps) handleImportSignals(w http.ResponseWriter, r *http.Request) {
	if d.Registry == nil || d.PlatformsDir == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "signals cannot be saved here"})
		return
	}
	platform := r.URL.Query().Get("platform")
	if err := d.designablePlatform(platform); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "could not read that file"})
		return
	}
	defs, err := signal.ParseExport([]byte(body.Text), platform)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	who := sessionName(r)
	imported, skipped := []string{}, []string{}
	var saved []*signal.Definition
	for _, def := range defs {
		if d.defined(platform, def.Name) {
			skipped = append(skipped, def.Name)
			continue
		}
		def.CreatedBy, def.UpdatedBy = who, who
		if err := signal.Save(d.PlatformsDir, *def); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": def.Name + ": " + err.Error()})
			return
		}
		imported = append(imported, def.Name)
		saved = append(saved, def)
	}
	if len(saved) > 0 {
		if _, err := signal.Reload(d.PlatformsDir, platform, d.Registry); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	for _, def := range saved {
		if d.Core == nil {
			break
		}
		d.Core.ForgetCandidate(platform, def.Name)
		if def.PacketType != "" {
			d.Core.ForgetCandidate(platform, def.PacketType)
		}
	}
	d.Log.Info("signals imported", "platform", platform, "imported", imported, "skipped", skipped, "by", who)
	writeJSON(w, http.StatusOK, map[string]any{"imported": imported, "skipped": skipped})
}
