package api

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/alert"
	"github.com/Sznapsollo/health-monitor-nj/internal/protocol"
	"github.com/Sznapsollo/health-monitor-nj/internal/signal"
	"github.com/Sznapsollo/health-monitor-nj/internal/silence"
	"github.com/Sznapsollo/health-monitor-nj/internal/state"
	"github.com/Sznapsollo/health-monitor-nj/internal/store"
)

// handleAlerts is the alerts panel's list, with the filters the old UI had.
func (d Deps) handleAlerts(w http.ResponseWriter, r *http.Request) {
	if d.Alerts == nil {
		writeJSON(w, http.StatusOK, map[string]any{"alerts": []any{}})
		return
	}
	platform := d.platformOf(r)

	// A named day reads the history on disk; without one the answer is the
	// live window in memory, which is what the tab shows by default.
	if day := r.URL.Query().Get("day"); day != "" {
		d.handleAlertHistory(w, r, platform, day)
		return
	}

	filter := alertFilter(r)
	list := d.Alerts.List(platform, filter)

	if strings.EqualFold(r.URL.Query().Get("format"), "csv") {
		writeAlertsCSV(w, list)
		return
	}
	out := map[string]any{
		"platform":   platform,
		"alerts":     list,
		"categories": d.Alerts.Categories(platform),
		"counts":     d.Alerts.Counts(platform),
	}
	// Which past days can be looked at, so the picker is useful before
	// anything has been picked.
	if d.DB != nil {
		if days, err := d.DB.AlertDays(r.Context(), platform); err == nil {
			out["days"] = days
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// handleAlertHistory answers from alert_history: the same rows the tab shows
// live, for a day that has already gone by.
func (d Deps) handleAlertHistory(w http.ResponseWriter, r *http.Request, platform, day string) {
	if d.DB == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "no alert history"})
		return
	}
	at, err := time.Parse("20060102", day)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "not a day"})
		return
	}
	q := r.URL.Query()
	query := store.AlertQuery{
		Platform: platform,
		From:     at,
		To:       at.Add(24 * time.Hour),
		Category: q.Get("category"),
		Text:     q.Get("text"),
		Silenced: q.Get("silenced") == "true",
		Limit:    atoi(q.Get("limit")),
	}
	for _, l := range splitList(q.Get("levels")) {
		query.Levels = append(query.Levels, strings.ToUpper(l))
	}

	rows, err := d.DB.SearchAlerts(r.Context(), query)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	days, err := d.DB.AlertDays(r.Context(), platform)
	if err != nil {
		days = nil
	}

	// The panel draws alerts, not rows: the shapes are deliberately the same,
	// so history needs no second renderer.
	list := make([]alert.Alert, 0, len(rows))
	counts := map[protocol.Level]int64{}
	categories := map[string]bool{}
	for _, row := range rows {
		list = append(list, alert.Alert{
			ID: row.ID, Platform: row.Platform, Signal: row.Signal,
			Level: protocol.Level(row.Level), Category: row.Category, Message: row.Message,
			GroupKey: row.GroupKey, Source: row.Source, Target: row.Target,
			Count: row.Count, First: row.First, Last: row.Last, Data: row.Data,
			Silenced: row.Silenced, SilenceReason: row.SilenceReason,
		})
		counts[protocol.Level(row.Level)] += row.Count
		if row.Category != "" {
			categories[row.Category] = true
		}
	}
	if strings.EqualFold(q.Get("format"), "csv") {
		writeAlertsCSV(w, list)
		return
	}

	names := make([]string, 0, len(categories))
	for name := range categories {
		names = append(names, name)
	}
	sort.Strings(names)

	writeJSON(w, http.StatusOK, map[string]any{
		"platform":   platform,
		"day":        day,
		"alerts":     list,
		"categories": names,
		"counts":     counts,
		"days":       days,
		"history":    true,
	})
}

func alertFilter(r *http.Request) alert.Filter {
	q := r.URL.Query()
	f := alert.Filter{
		Text:            q.Get("text"),
		Limit:           atoi(q.Get("limit")),
		IncludeSilenced: q.Get("silenced") == "true",
	}
	for _, l := range splitList(q.Get("levels")) {
		f.Levels = append(f.Levels, protocol.Level(strings.ToUpper(l)))
	}
	f.Categories = splitList(q.Get("categories"))
	if ms, err := strconv.ParseFloat(q.Get("minMs"), 64); err == nil {
		f.MinLatencyMS = ms
	}
	if mins := atoi(q.Get("minutes")); mins > 0 {
		f.Since = time.Now().Add(-time.Duration(mins) * time.Minute)
	}
	return f
}

func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// writeAlertsCSV is the old panel's export, over whatever filter is applied.
func writeAlertsCSV(w http.ResponseWriter, list []alert.Alert) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="alerts.csv"`)

	c := csv.NewWriter(w)
	defer c.Flush()
	_ = c.Write([]string{"first", "last", "level", "category", "count", "message", "source", "silenced", "reason"})
	for _, a := range list {
		_ = c.Write([]string{
			a.First.Format(time.RFC3339), a.Last.Format(time.RFC3339),
			string(a.Level), a.Category, strconv.FormatInt(a.Count, 10),
			a.Message, a.Source, strconv.FormatBool(a.Silenced), a.SilenceReason,
		})
	}
}

// handleStatus lists what reports its state, which is the old "Status app /
// jobów / mail server" viewers as one table.
func (d Deps) handleStatus(w http.ResponseWriter, r *http.Request) {
	if d.Statuses == nil {
		writeJSON(w, http.StatusOK, map[string]any{"entities": []any{}})
		return
	}
	platform := d.platformOf(r)
	online, offline := d.Statuses.Counts(platform)
	writeJSON(w, http.StatusOK, map[string]any{
		"platform": platform,
		"entities": d.Statuses.List(platform),
		"online":   online,
		"offline":  offline,
		"packets":  d.packetsLastMinute(platform),
	})
}

// packetsLastMinute is what each sender sent in the last complete minute, by
// the name its packets carry, which is also its key on the status list.
func (d Deps) packetsLastMinute(platform string) map[string]int64 {
	out := map[string]int64{}
	if d.Store == nil {
		return out
	}
	view, ok := d.Store.View(platform, signal.PacketsSignal, state.Criteria{
		HistoryMinutes: 3, GroupMinutes: 3, Group: "sender", GroupTop: 1000,
	})
	if !ok {
		return out
	}
	last := state.MinuteOf(time.Now()) - 1
	for _, g := range view.Groups {
		for _, p := range g.Points {
			if p.Minute == last {
				out[g.Value] = p.Count
			}
		}
	}
	return out
}

// handleForget removes an entity for good — the proper replacement for the old
// removeAdditionalDataByPath.
func (d Deps) handleForget(w http.ResponseWriter, r *http.Request) {
	if d.Statuses == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "status tracking is not available"})
		return
	}
	var body struct {
		Platform string `json:"platform"`
		Signal   string `json:"signal"`
		Key      string `json:"key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "could not read that request"})
		return
	}
	if body.Signal == "" || body.Key == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "signal and key are required"})
		return
	}
	if body.Platform == "" {
		body.Platform = d.platformOf(r)
	}
	if err := d.Statuses.Forget(r.Context(), body.Platform, body.Signal, body.Key); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	d.Log.Info("entity forgotten", "platform", body.Platform, "signal", body.Signal, "key", body.Key)
	writeJSON(w, http.StatusOK, map[string]string{"status": "forgotten"})
}

// handleSilences lists what is currently silenced, so nothing is quiet without
// being visible.
func (d Deps) handleSilences(w http.ResponseWriter, r *http.Request) {
	if d.Silences == nil {
		writeJSON(w, http.StatusOK, map[string]any{"silences": []any{}})
		return
	}
	platform := d.platformOf(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"platform": platform,
		"silences": d.Silences.List(r.Context(), platform),
		"review":   d.Silences.NeedingReview(),
	})
}

// handleCreateSilence records a silence. A reason is required by the store.
// readSilence reads what a silence should be from a create or edit request.
func (d Deps) readSilence(w http.ResponseWriter, r *http.Request) (silence.Silence, bool) {
	var body struct {
		Platform string `json:"platform"`
		Target   string `json:"target"`
		Kind     string `json:"kind"`
		Reason   string `json:"reason"`
		By       string `json:"by"`
		Until    string `json:"until"`
		// Minutes is a convenience for the quick choices in the UI.
		Minutes int `json:"minutes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "could not read that request"})
		return silence.Silence{}, false
	}
	if body.Platform == "" {
		body.Platform = d.platformOf(r)
	}
	in := silence.Silence{
		Platform: body.Platform,
		Target:   body.Target,
		Kind:     silence.Kind(body.Kind),
		Reason:   body.Reason,
		By:       body.By,
	}
	switch {
	case body.Minutes > 0:
		in.Until = time.Now().Add(time.Duration(body.Minutes) * time.Minute)
	case body.Until != "":
		until, err := time.Parse(time.RFC3339, body.Until)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "until is not a time"})
			return silence.Silence{}, false
		}
		in.Until = until
	}
	return in, true
}

func (d Deps) reapplyAllSilences() int {
	n := 0
	if d.Core != nil && d.Registry != nil {
		for _, platform := range d.Registry.Platforms() {
			n += d.Core.ReapplySilences(platform)
		}
	}
	return n
}

func (d Deps) handleUpdateSilence(w http.ResponseWriter, r *http.Request) {
	if d.Silences == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "silencing is not available"})
		return
	}
	in, ok := d.readSilence(w, r)
	if !ok {
		return
	}
	updated, err := d.Silences.Update(r.Context(), r.PathValue("id"), in)
	if errors.Is(err, silence.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	changed := d.reapplyAllSilences()
	d.Log.Info("silence edited", "id", updated.ID, "target", updated.Target, "kind", updated.Kind,
		"by", sessionName(r), "changed", changed)
	writeJSON(w, http.StatusOK, updated)
}

func (d Deps) handleCreateSilence(w http.ResponseWriter, r *http.Request) {
	if d.Silences == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "silencing is not available"})
		return
	}
	in, ok := d.readSilence(w, r)
	if !ok {
		return
	}
	if strings.TrimSpace(in.By) == "" {
		if session, ok := SessionOf(r); ok {
			in.By = session.Name
		}
	}

	created, err := d.Silences.Create(r.Context(), in)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	hidden := 0
	if d.Core != nil {
		hidden = d.Core.ReapplySilences(created.Platform)
	}
	d.Log.Info("silence created", "target", created.Target, "kind", created.Kind,
		"reason", created.Reason, "by", created.By, "hidden", hidden)
	writeJSON(w, http.StatusCreated, struct {
		silence.Silence
		Hidden int `json:"hidden"`
	}{created, hidden})
}

// handleDeleteSilence is the one-click un-silence.
func (d Deps) handleDeleteSilence(w http.ResponseWriter, r *http.Request) {
	if d.Silences == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "silencing is not available"})
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "an id is required"})
		return
	}
	if err := d.Silences.Delete(r.Context(), id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	d.Log.Info("silence removed", "id", id, "shown", d.reapplyAllSilences())
	writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})
}

// platformOf decides which platform a request is about. The registry is asked
// first: it knows every platform from the catalogues, while the hot state only
// knows the ones that have received data.
func (d Deps) platformOf(r *http.Request) string {
	if p := r.URL.Query().Get("platform"); p != "" {
		return p
	}
	if d.Registry != nil {
		if ps := d.Registry.Platforms(); len(ps) > 0 {
			return ps[0]
		}
	}
	if d.Store != nil {
		if ps := d.Store.Platforms(); len(ps) > 0 {
			return ps[0]
		}
	}
	return ""
}
