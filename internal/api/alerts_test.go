package api_test

import (
	"context"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/alert"
	"github.com/Sznapsollo/health-monitor-nj/internal/api"
	"github.com/Sznapsollo/health-monitor-nj/internal/protocol"
	"github.com/Sznapsollo/health-monitor-nj/internal/signal"
	"github.com/Sznapsollo/health-monitor-nj/internal/state"
	"github.com/Sznapsollo/health-monitor-nj/internal/store"
	"github.com/prometheus/client_golang/prometheus"
)

// A server with both halves of the alerts picture: the live store in memory
// and the history on disk.
func alertsFixture(t *testing.T) (*httptest.Server, *alert.Store, *store.Store) {
	t.Helper()
	reg := signal.NewRegistry()
	hot := state.NewStore(reg, func() time.Time { return now })
	db, err := store.Open(store.Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	alerts := alert.NewStore(alert.Options{Now: func() time.Time { return now }})
	srv := httptest.NewServer(api.Handler(api.Deps{
		Log:      slog.New(slog.NewTextHandler(discard{}, nil)),
		Metrics:  prometheus.NewRegistry(),
		Registry: reg,
		Store:    hot,
		DB:       db,
		Alerts:   alerts,
		Started:  now,
	}))
	t.Cleanup(srv.Close)
	return srv, alerts, db
}

func TestAlertsEndpointReadsLiveOrHistory(t *testing.T) {
	srv, alerts, db := alertsFixture(t)
	ctx := context.Background()

	alerts.Add(alert.Alert{
		Platform: "test", Level: protocol.LevelError, Message: "happening now",
		Category: "jobs", Last: now,
	})

	old := now.AddDate(0, 0, -3)
	err := db.WriteAlerts(ctx, []store.AlertRow{{
		ID: "a1", Platform: "test", Level: "ERROR", Category: "jobs",
		Message: "happened on Tuesday", Count: 7, First: old, Last: old,
	}})
	if err != nil {
		t.Fatal(err)
	}

	// No day named: the live window, as the tab has always shown it — plus
	// the days that can be looked back at.
	live := getJSON(t, srv, "/api/alerts?platform=test")
	rows, _ := live["alerts"].([]any)
	if len(rows) != 1 {
		t.Fatalf("live alerts = %v", live["alerts"])
	}
	if first, _ := rows[0].(map[string]any); first["message"] != "happening now" {
		t.Errorf("live = %v, want what is in memory", rows[0])
	}
	days, _ := live["days"].([]any)
	if len(days) != 1 || days[0] != old.UTC().Format("20060102") {
		t.Errorf("days = %v, want the day the history holds", live["days"])
	}

	// A day named: the history, in the same shape, so the panel needs no
	// second renderer.
	past := getJSON(t, srv, "/api/alerts?platform=test&day="+old.UTC().Format("20060102"))
	rows, _ = past["alerts"].([]any)
	if len(rows) != 1 {
		t.Fatalf("history alerts = %v", past["alerts"])
	}
	got, _ := rows[0].(map[string]any)
	if got["message"] != "happened on Tuesday" {
		t.Errorf("history = %v, want the recorded burst", got)
	}
	if got["count"] != float64(7) {
		t.Errorf("count = %v, want the burst's total", got["count"])
	}
	if past["history"] != true {
		t.Errorf("history flag = %v", past["history"])
	}

	// A day with nothing recorded is empty, not an error.
	empty := getJSON(t, srv, "/api/alerts?platform=test&day=20200101")
	if rows, _ := empty["alerts"].([]any); len(rows) != 0 {
		t.Errorf("alerts = %v, want none", empty["alerts"])
	}
}
