package api

import (
	"net/http"
	"strconv"

	"github.com/Sznapsollo/health-monitor-nj/internal/signal"
	"github.com/Sznapsollo/health-monitor-nj/internal/state"
)

// catalogueResponse tells the browser what exists and how it should be drawn,
// so the pickers and tiles are built from the platform's own definitions
// rather than from anything hard-coded in the UI.
type catalogueResponse struct {
	Platforms []platformInfo `json:"platforms"`
}

type platformInfo struct {
	Name    string       `json:"name"`
	Signals []signalInfo `json:"signals"`
}

type signalInfo struct {
	Name           string            `json:"name"`
	Kind           string            `json:"kind"`
	DisplayName    string            `json:"displayName"`
	Dims           []dimInfo         `json:"dims"`
	Views          []signal.View     `json:"views,omitempty"`
	Retention      retentionInfo     `json:"retention"`
	AutoRegistered bool              `json:"autoRegistered,omitempty"`
	BuiltIn        bool              `json:"builtIn,omitempty"`
	Values         map[string]string `json:"values,omitempty"`
	TTLSeconds     int               `json:"ttlSeconds,omitempty"`
	MaxKeys        int               `json:"maxKeys,omitempty"`
	ReadOnly       bool              `json:"readOnly,omitempty"`
	CreatedBy      string            `json:"createdBy,omitempty"`
	UpdatedBy      string            `json:"updatedBy,omitempty"`
	KeepPairs      [][2]string       `json:"keepPairs,omitempty"`
	PacketType     string            `json:"packetType,omitempty"`
	Merge          bool              `json:"merge,omitempty"`
	NoStatus       bool              `json:"noStatus,omitempty"`
	Colors         map[string]string `json:"colors,omitempty"`
}

type dimInfo struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	LabelDim    string `json:"labelDim,omitempty"`
}

type retentionInfo struct {
	HotDetailMinutes int  `json:"hotDetailMinutes"`
	HotTotalsMinutes int  `json:"hotTotalsMinutes"`
	DurableDays      int  `json:"durableDays"`
	DetailDays       int  `json:"detailDays"`
	LogDays          int  `json:"logDays,omitempty"`
	ArchiveDays      *int `json:"archiveDays,omitempty"`
	Versions         int  `json:"versions,omitempty"`
}

func (d Deps) handleCatalogue(w http.ResponseWriter, r *http.Request) {
	if d.Registry == nil {
		writeJSON(w, http.StatusOK, catalogueResponse{})
		return
	}
	out := catalogueResponse{}
	for _, platform := range d.Registry.Platforms() {
		info := platformInfo{Name: platform}
		for _, def := range d.Registry.Definitions(platform) {
			info.Signals = append(info.Signals, infoOf(def))
		}
		out.Platforms = append(out.Platforms, info)
	}
	writeJSON(w, http.StatusOK, out)
}

func infoOf(def *signal.Definition) signalInfo {
	s := signalInfo{
		Dims:           []dimInfo{},
		Name:           def.Name,
		Kind:           string(def.Kind),
		DisplayName:    def.Display.Name,
		Views:          def.Views,
		AutoRegistered: def.AutoRegistered,
		BuiltIn:        def.BuiltIn,
		TTLSeconds:     def.TTLSeconds,
		MaxKeys:        def.MaxKeys,
		ReadOnly:       def.ReadOnly,
		CreatedBy:      def.CreatedBy,
		UpdatedBy:      def.UpdatedBy,
		KeepPairs:      def.KeepPairs,
		PacketType:     def.PacketType,
		Merge:          def.Merge,
		NoStatus:       def.NoStatus,
		Colors:         def.Display.Colors,
		Retention: retentionInfo{
			HotDetailMinutes: def.Retention.HotDetailMinutes,
			HotTotalsMinutes: def.Retention.HotTotalsMinutes,
			DurableDays:      def.Retention.DurableDays,
			DetailDays:       def.Retention.DetailDays,
			LogDays:          def.Retention.LogDays,
			ArchiveDays:      def.Retention.ArchiveDays,
			Versions:         def.Retention.Versions,
		},
	}
	for _, dim := range def.Dims {
		s.Dims = append(s.Dims, dimInfo{Name: dim, DisplayName: def.Display.DimName(dim), LabelDim: def.Display.Labels[dim]})
	}
	if def.Values.Count != "" || def.Values.MS != "" {
		s.Values = map[string]string{"count": def.Values.Count, "ms": def.Values.MS}
	}
	return s
}

// handleSeries answers a chart's question directly. Windows that reach past
// the hot tiers are served from SQLite once the store lands; today the view
// says how far the detail actually goes.
func (d Deps) handleSeries(w http.ResponseWriter, r *http.Request) {
	if d.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "no hot state"})
		return
	}
	q := r.URL.Query()
	platform := d.platformOf(r)
	sig := q.Get("signal")
	if sig == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "signal is required"})
		return
	}

	c := state.Criteria{
		Signal:         sig,
		HistoryMinutes: atoi(q.Get("minutes")),
		Group:          q.Get("group"),
		GroupTop:       atoi(q.Get("top")),
		GroupFilter:    q.Get("filter"),
		Sub:            q.Get("sub"),
		SubTop:         atoi(q.Get("subTop")),
		SubFilter:      q.Get("subFilter"),
		SortBy:         q.Get("sort"),
	}
	// One window for everything here: the tiles are as long as the chart.
	c.GroupMinutes = c.Normalise().HistoryMinutes
	// A chart that asks for a pair makes the server start keeping it.
	if pair, ok := c.Pair(); ok {
		d.Store.AddActivePair(platform, sig, pair)
	}

	view, ok := d.Store.View(platform, sig, c)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": "no data for " + platform + "/" + sig,
		})
		return
	}
	writeJSON(w, http.StatusOK, d.withHistory(r.Context(), platform, c.Normalise(), view))
}

// handleGauges returns defined gauges only; an undefined one waits in the designer.
func (d Deps) handleGauges(w http.ResponseWriter, r *http.Request) {
	if d.Gauges == nil {
		writeJSON(w, http.StatusOK, map[string]any{"gauges": []any{}})
		return
	}
	platform := d.platformOf(r)
	names := d.Gauges.Signals(platform)
	views := make([]any, 0, len(names))
	for _, name := range names {
		if d.Registry != nil && !d.defined(platform, name) {
			continue
		}
		views = append(views, d.Gauges.View(platform, name))
	}
	writeJSON(w, http.StatusOK, map[string]any{"platform": platform, "gauges": views})
}

// handleDashboards returns the arrangements a platform defines.
func (d Deps) handleDashboards(w http.ResponseWriter, r *http.Request) {
	if d.Dashboards == nil {
		writeJSON(w, http.StatusOK, map[string]any{"dashboards": []any{}})
		return
	}
	platform := d.platformOf(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"platform":   platform,
		"dashboards": d.Dashboards.For(platform),
	})
}

// handleState is the HM-errors view: what the hot state holds, what could not
// be placed, and what has not been charted yet.
func (d Deps) handleState(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{}
	if d.Store != nil {
		out["signals"] = d.Store.Stats()
		out["platforms"] = d.Store.Platforms()
		out["hotBytes"] = d.Store.EstimatedBytes()
		out["hotEvictions"] = d.Store.Evictions()
	}
	if d.Core != nil {
		out["quarantine"] = d.Core.QuarantineSnapshot()
		out["pendingEnvelopes"] = d.Core.PendingCounts()
	}
	if d.Intake != nil {
		out["intake"] = d.Intake.Counters()
	}
	if d.Writer != nil {
		out["writer"] = d.Writer.Stats()
	}
	if d.LogWriter != nil {
		out["logWriter"] = d.LogWriter.Stats()
	}
	if d.AlertWriter != nil {
		out["alertWriter"] = d.AlertWriter.Stats()
	}
	if d.Statuses != nil {
		out["statusWriter"] = d.Statuses.Stats()
	}
	if d.DB != nil {
		if counts, err := d.DB.Counts(r.Context()); err == nil {
			out["durableRows"] = counts
		}
		if d.DB.Path() != "" {
			out["dbBytes"] = d.DB.Bytes()
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func atoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}
