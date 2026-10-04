package api_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/api"
	"github.com/Sznapsollo/health-monitor-nj/internal/core"
	"github.com/Sznapsollo/health-monitor-nj/internal/signal"
	"github.com/Sznapsollo/health-monitor-nj/internal/state"
	"github.com/prometheus/client_golang/prometheus"
)

func rulesFixture(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	reg := signal.NewRegistry()
	if err := reg.Add(&signal.Definition{
		Platform: "test", Name: "requests", Kind: signal.KindTimeseries, Dims: []string{"url"},
	}); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(discard{}, nil))
	hot := state.NewStore(reg, func() time.Time { return now })
	dir := t.TempDir()
	srv := httptest.NewServer(api.Handler(api.Deps{
		Log: log, Metrics: prometheus.NewRegistry(), Registry: reg, Store: hot,
		Core:         core.New(hot, reg, log, func() time.Time { return now }),
		PlatformsDir: dir, Started: now,
	}))
	t.Cleanup(srv.Close)
	return srv, dir
}

func putRules(t *testing.T, url string, body any) *http.Response {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestRulesAreSavedUnderTheirPlatform(t *testing.T) {
	srv, dir := rulesFixture(t)
	res := putRules(t, srv.URL+"/api/rules", map[string]any{
		"platform": "test",
		"latency":  map[string]any{"defaultMs": 1500, "level": "warn"},
	})
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(dir, "test", "rules.yaml")); err != nil {
		t.Fatalf("rules.yaml was not written: %v", err)
	}
	var saved struct {
		Latency struct {
			DefaultMS float64 `json:"defaultMs"`
			Level     string  `json:"level"`
		} `json:"latency"`
	}
	if err := json.NewDecoder(res.Body).Decode(&saved); err != nil {
		t.Fatal(err)
	}
	if saved.Latency.DefaultMS != 1500 || saved.Latency.Level != "WARN" {
		t.Errorf("saved = %+v, want the threshold and the level spelled the way the file spells it", saved.Latency)
	}
}

func TestRulesRefuseAPlatformThatIsNotDeclared(t *testing.T) {
	srv, dir := rulesFixture(t)
	for _, platform := range []string{"../escape", "nobody", ""} {
		res := putRules(t, srv.URL+"/api/rules", map[string]any{
			"platform": platform,
			"latency":  map[string]any{"defaultMs": 1500},
		})
		_ = res.Body.Close()
		want := http.StatusBadRequest
		if platform == "" {
			want = http.StatusOK
		}
		if res.StatusCode != want {
			t.Errorf("platform %q: status = %d, want %d", platform, res.StatusCode, want)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "escape", "rules.yaml")); err == nil {
		t.Fatal("a rules file was written outside the platforms directory")
	}
	if _, err := os.Stat(filepath.Join(dir, "nobody")); err == nil {
		t.Fatal("a directory was created for a platform nobody declared")
	}
}

func TestTestingRulesWithNothingToTestAnswersEmptyLists(t *testing.T) {
	srv, _ := archiveFixture(t)
	res, err := srv.Client().Post(srv.URL+"/api/rules/test?platform=test", "application/json",
		strings.NewReader(`{"platform":"test","latency":{"defaultMs":1000},"offline":{"afterSeconds":300},"groups":{"windowSeconds":60}}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(body), `"byRule":[]`) || !strings.Contains(string(body), `"examples":[]`) {
		t.Errorf("status %d, body %s", res.StatusCode, body)
	}
}
