package api

import (
	"encoding/json"
	"net/http"

	"github.com/Sznapsollo/health-monitor-nj/internal/dashboard"
)

func (d Deps) handlePutDashboard(w http.ResponseWriter, r *http.Request) {
	if d.Dashboards == nil || d.PlatformsDir == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "dashboards cannot be saved here"})
		return
	}
	var body dashboard.Dashboard
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "could not read that dashboard"})
		return
	}
	body.ID = r.PathValue("id")
	if body.Platform == "" {
		body.Platform = d.platformOf(r)
	}
	if !d.knownPlatform(body.Platform) {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "no platform " + body.Platform + " is declared under " + d.PlatformsDir,
		})
		return
	}
	if err := dashboard.ValidID(body.ID); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if body.Name == "" {
		body.Name = body.ID
	}

	who := "someone"
	if session, ok := SessionOf(r); ok && session.Name != "" {
		who = session.Name
	}
	body.ReadOnly = false
	body.UpdatedBy = who
	body.CreatedBy = who
	for _, have := range d.Dashboards.For(body.Platform) {
		if have.ID != body.ID {
			continue
		}
		if have.ReadOnly {
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": body.ID + " comes from dashboards.yaml; duplicate it under a new name instead",
			})
			return
		}
		if have.CreatedBy != "" {
			body.CreatedBy = have.CreatedBy
		}
	}
	body.Normalise()
	if err := body.Validate(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := dashboard.Save(d.PlatformsDir, body); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := d.reloadDashboards(body.Platform); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	d.Log.Info("dashboard saved", "platform", body.Platform, "id", body.ID, "by", who)
	writeJSON(w, http.StatusOK, body)
}

func (d Deps) handleDeleteDashboard(w http.ResponseWriter, r *http.Request) {
	if d.Dashboards == nil || d.PlatformsDir == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "dashboards cannot be deleted here"})
		return
	}
	id := r.PathValue("id")
	platform := d.platformOf(r)
	if !d.knownPlatform(platform) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no platform " + platform})
		return
	}
	if err := dashboard.ValidID(id); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	for _, have := range d.Dashboards.For(platform) {
		if have.ID == id && have.ReadOnly {
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": id + " comes from dashboards.yaml and can only be removed there",
			})
			return
		}
	}
	if d.Auth != nil {
		for _, token := range d.Auth.DisplayTokens() {
			if token.Platform == platform && token.Dashboard == id {
				writeJSON(w, http.StatusConflict, map[string]string{
					"error": "the wall display " + token.Name + " is paired with " + id + "; re-point or revoke it first",
				})
				return
			}
		}
	}
	if err := dashboard.Delete(d.PlatformsDir, platform, id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := d.reloadDashboards(platform); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	d.Log.Info("dashboard deleted", "platform", platform, "id", id)
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted", "id": id})
}

// reloadDashboards applies a change at once rather than waiting for the watcher.
func (d Deps) reloadDashboards(platform string) error {
	list, err := dashboard.LoadPlatform(d.PlatformsDir, platform)
	if err != nil {
		return err
	}
	d.Dashboards.Replace(platform, list)
	return nil
}
