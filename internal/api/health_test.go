package api_test

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/api"
	"github.com/Sznapsollo/health-monitor-nj/internal/auth"
	"github.com/Sznapsollo/health-monitor-nj/internal/core"
	"github.com/Sznapsollo/health-monitor-nj/internal/gauge"
	"github.com/Sznapsollo/health-monitor-nj/internal/health"
	"github.com/Sznapsollo/health-monitor-nj/internal/protocol"
	"github.com/Sznapsollo/health-monitor-nj/internal/signal"
	"github.com/Sznapsollo/health-monitor-nj/internal/state"
	"github.com/Sznapsollo/health-monitor-nj/internal/store"
	"github.com/prometheus/client_golang/prometheus"
)

type healthBody struct {
	Issues  []health.Issue `json:"issues"`
	Unknown int            `json:"unknown"`
}

func healthOf(t *testing.T, c *http.Client, method, url string, payload any) healthBody {
	t.Helper()
	res := do(t, c, method, url, payload)
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	var body healthBody
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestHealthGroupsAndMarksKnown(t *testing.T) {
	reg := signal.NewRegistry()
	hot := state.NewStore(reg, func() time.Time { return now })
	brain := core.New(hot, reg, slog.New(slog.NewTextHandler(discard{}, nil)), func() time.Time { return now })
	db, err := store.Open(store.Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	manager, err := auth.Open(auth.Options{DB: db.DB(), Password: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	gauges := gauge.NewStore(func() time.Time { return now }, 0)
	gauges.Observe("demo", "diskFree", "shop-1", []gauge.Point{{Label: "data", Value: 3}}, now)
	gauges.Observe("demo", "diskFree", "shop-1", []gauge.Point{{Label: "data", Value: 4}}, now)
	book, err := health.Open(db.DB(), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(api.Handler(api.Deps{
		Log: slog.New(slog.NewTextHandler(discard{}, nil)), Metrics: prometheus.NewRegistry(),
		Registry: reg, Store: hot, Core: brain, Auth: manager, Health: book, Gauges: gauges, Started: now,
	}))
	t.Cleanup(srv.Close)
	c := client(t)
	_ = post(t, c, srv.URL+"/api/login", map[string]string{"name": "anna", "password": "pw"}).Body.Close()

	gaugeHeader := protocol.Header{V: 1, T: protocol.TypeGauge, Platform: "demo", Source: "vpn-1", TS: protocol.NewTimestamp(now)}
	brain.Quarantine([]protocol.Packet{
		unknownMetric("demo", "checkout", "eu", 20),
		unknownMetric("demo", "checkout", "us", 40),
		{Header: gaugeHeader, Gauge: &protocol.Gauge{Header: gaugeHeader, Signal: "vpnUsage"}},
	}, "unknown_signal")
	brain.Quarantine(nil, "decode_error")

	got := healthOf(t, c, http.MethodGet, srv.URL+"/api/health", nil)
	if len(got.Issues) != 4 || got.Unknown != 4 {
		t.Fatalf("issues = %+v", got)
	}
	byKey := map[string]health.Issue{}
	for _, is := range got.Issues {
		byKey[is.Key] = is
	}
	checkout := byKey["unknown_signal/demo/checkout/metric"]
	if checkout.Count != 2 || checkout.Type != "metric" || checkout.Known {
		t.Errorf("checkout = %+v", checkout)
	}
	if g := byKey["unknown_signal/demo/vpnUsage/gauge"]; g.Type != "gauge" || g.LastSource != "vpn-1" {
		t.Errorf("gauge = %+v", g)
	}
	if g := byKey["unknown_signal/demo/diskFree/gauge"]; g.Count != 2 || g.LastSource != "shop-1" {
		t.Errorf("reporting gauge = %+v", g)
	}

	got = healthOf(t, c, http.MethodPost, srv.URL+"/api/health/known",
		map[string]any{"keys": []string{checkout.Key}})
	if got.Unknown != 3 {
		t.Errorf("unknown after marking = %d", got.Unknown)
	}
	brain.Quarantine([]protocol.Packet{unknownMetric("demo", "checkout", "eu", 20)}, "unknown_signal")
	got = healthOf(t, c, http.MethodGet, srv.URL+"/api/health", nil)
	for _, is := range got.Issues {
		if is.Key == checkout.Key && (!is.Known || is.KnownBy != "anna" || is.Count != 3) {
			t.Errorf("known checkout = %+v", is)
		}
	}

	got = healthOf(t, c, http.MethodPost, srv.URL+"/api/health/known",
		map[string]any{"keys": []string{checkout.Key}, "known": false})
	if got.Unknown != 4 {
		t.Errorf("unknown after unmarking = %d", got.Unknown)
	}
}
