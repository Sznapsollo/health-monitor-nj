package api_test

import (
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/api"
	"github.com/prometheus/client_golang/prometheus"
)

func newHandler(assets fs.FS, has bool) http.Handler {
	return api.Handler(api.Deps{
		Log:       slog.New(slog.NewTextHandler(discard{}, nil)),
		Metrics:   prometheus.NewRegistry(),
		Assets:    assets,
		HasAssets: has,
		Started:   time.Now().Add(-time.Minute),
	})
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

func TestHealthz(t *testing.T) {
	rec := httptest.NewRecorder()
	newHandler(nil, false).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("body = %v", body)
	}
	if body["uptimeSeconds"].(float64) < 1 {
		t.Fatalf("uptime = %v", body["uptimeSeconds"])
	}
}

func TestMetricsEndpoint(t *testing.T) {
	rec := httptest.NewRecorder()
	newHandler(nil, false).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestSPAFallsBackToIndex(t *testing.T) {
	assets := fstest.MapFS{
		"index.html":     {Data: []byte("<!doctype html>app")},
		"assets/main.js": {Data: []byte("console.log(1)")},
	}
	h := newHandler(assets, true)

	for _, path := range []string{"/", "/alerts", "/search?x=1"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK || rec.Body.String() != "<!doctype html>app" {
			t.Fatalf("%s: status %d body %q", path, rec.Code, rec.Body.String())
		}
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/assets/main.js", nil))
	if rec.Body.String() != "console.log(1)" {
		t.Fatalf("asset body = %q", rec.Body.String())
	}
}

func TestSPAWithoutBuildExplainsItself(t *testing.T) {
	rec := httptest.NewRecorder()
	newHandler(nil, false).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
}
