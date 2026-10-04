package api

import (
	"encoding/json"
	"net/http"
)

// handleMessage is the old "send message to users" action: a line of text
// pushed to every connected viewer. A wall display shows it briefly and never
// waits for someone to dismiss it.
func (d Deps) handleMessage(w http.ResponseWriter, r *http.Request) {
	if d.Hub == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "no viewers to tell"})
		return
	}
	var body struct {
		Text  string `json:"text"`
		Level string `json:"level"`
		Popup bool   `json:"popup"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "could not read that request"})
		return
	}
	if body.Text == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "a message needs some text"})
		return
	}
	if body.Level == "" {
		body.Level = "info"
	}

	from := "someone"
	if session, ok := SessionOf(r); ok && session.Name != "" {
		from = session.Name
	}
	d.Hub.Notice(body.Level, body.Text, body.Popup)
	d.Log.Info("message sent to viewers", "from", from, "text", body.Text)
	writeJSON(w, http.StatusOK, map[string]any{"sent": true, "viewers": len(d.Hub.Sessions())})
}
