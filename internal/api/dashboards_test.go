package api_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/api"
	"github.com/Sznapsollo/health-monitor-nj/internal/auth"
	"github.com/Sznapsollo/health-monitor-nj/internal/dashboard"
	"github.com/Sznapsollo/health-monitor-nj/internal/signal"
	"github.com/Sznapsollo/health-monitor-nj/internal/state"
	"github.com/Sznapsollo/health-monitor-nj/internal/store"
	"github.com/prometheus/client_golang/prometheus"
)

func dashboardFixture(t *testing.T) (*httptest.Server, *dashboard.Store, string) {
	t.Helper()
	reg := signal.NewRegistry()
	for _, def := range []*signal.Definition{
		{Platform: "test", Name: "requests", Kind: signal.KindTimeseries, Dims: []string{"url"}},
		{Platform: "web", Name: "requests", Kind: signal.KindTimeseries, Dims: []string{"url"}},
		{Platform: "web", Name: "serverLoad", Kind: signal.KindGauge},
	} {
		if err := reg.Add(def); err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "test"), 0o755); err != nil {
		t.Fatal(err)
	}
	shipped := "dashboards:\n  ops:\n    name: Ops\n    columns:\n      - panels:\n          - type: status\n"
	if err := os.WriteFile(filepath.Join(dir, "test", "dashboards.yaml"), []byte(shipped), 0o600); err != nil {
		t.Fatal(err)
	}
	boards := dashboard.NewStore()
	list, err := dashboard.LoadPlatform(dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	boards.Replace("test", list)

	db, err := store.Open(store.Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	manager, err := auth.Open(auth.Options{DB: db.DB(), Password: "pw"})
	if err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(api.Handler(api.Deps{
		Log:          slog.New(slog.NewTextHandler(discard{}, nil)),
		Metrics:      prometheus.NewRegistry(),
		Registry:     reg,
		Store:        state.NewStore(reg, func() time.Time { return now }),
		Dashboards:   boards,
		Auth:         manager,
		PlatformsDir: dir,
		Started:      now,
	}))
	t.Cleanup(srv.Close)
	return srv, boards, dir
}

func do(t *testing.T, c *http.Client, method, url string, body any) *http.Response {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(encoded)
	} else {
		reader = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestDashboardsAreSavedAndDeleted(t *testing.T) {
	srv, boards, dir := dashboardFixture(t)
	c := client(t)
	_ = post(t, c, srv.URL+"/api/login", map[string]string{"name": "anna", "password": "pw"}).Body.Close()

	body := map[string]any{
		"name":    "Anna's view",
		"columns": []any{map[string]any{"panels": []any{map[string]any{"type": "chart", "signal": "requests", "group": "url"}}}},
	}
	res := do(t, c, http.MethodPut, srv.URL+"/api/dashboards/anna-view", body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("put: %d", res.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(dir, "test", "dashboards", "anna-view.yaml")); err != nil {
		t.Fatal(err)
	}
	list := boards.For("test")
	if len(list) != 2 {
		t.Fatalf("store has %d dashboards, want the shipped one plus the new one", len(list))
	}
	var made dashboard.Dashboard
	for _, d := range list {
		if d.ID == "anna-view" {
			made = d
		}
	}
	if made.CreatedBy != "anna" || made.UpdatedBy != "anna" || made.ReadOnly {
		t.Errorf("made = %+v", made)
	}

	// Another person edits it: the creator stays, the editor is recorded.
	other := client(t)
	_ = post(t, other, srv.URL+"/api/login", map[string]string{"name": "bob", "password": "pw"}).Body.Close()
	res = do(t, other, http.MethodPut, srv.URL+"/api/dashboards/anna-view", body)
	_ = res.Body.Close()
	for _, d := range boards.For("test") {
		if d.ID == "anna-view" && (d.CreatedBy != "anna" || d.UpdatedBy != "bob") {
			t.Errorf("after bob's edit = %+v", d)
		}
	}

	// The shipped one cannot be overwritten or removed from here.
	res = do(t, c, http.MethodPut, srv.URL+"/api/dashboards/ops", body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Errorf("put over shipped: %d, want 409", res.StatusCode)
	}
	res = do(t, c, http.MethodDelete, srv.URL+"/api/dashboards/ops", nil)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Errorf("delete shipped: %d, want 409", res.StatusCode)
	}

	// Nor can an id escape the directory, or name an unknown platform.
	res = do(t, c, http.MethodPut, srv.URL+"/api/dashboards/..%2Fescape", body)
	_ = res.Body.Close()
	if res.StatusCode == http.StatusOK {
		t.Error("a path was accepted as an id")
	}
	res = do(t, c, http.MethodPut, srv.URL+"/api/dashboards/x?platform=nobody", body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("unknown platform: %d, want 400", res.StatusCode)
	}

	// A screen paired with it keeps it alive.
	tok := post(t, c, srv.URL+"/api/display-tokens", map[string]string{
		"name": "kitchen", "platform": "test", "dashboard": "anna-view",
	})
	_ = tok.Body.Close()
	if tok.StatusCode != http.StatusCreated {
		t.Fatalf("token: %d", tok.StatusCode)
	}
	res = do(t, c, http.MethodDelete, srv.URL+"/api/dashboards/anna-view", nil)
	var problem map[string]string
	_ = json.NewDecoder(res.Body).Decode(&problem)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusConflict || problem["error"] == "" {
		t.Fatalf("delete while paired: %d %v, want 409 naming the screen", res.StatusCode, problem)
	}

	var tokens struct {
		Tokens []struct{ ID string } `json:"tokens"`
	}
	lst, _ := c.Get(srv.URL + "/api/display-tokens")
	_ = json.NewDecoder(lst.Body).Decode(&tokens)
	_ = lst.Body.Close()
	res = do(t, c, http.MethodDelete, srv.URL+"/api/display-tokens/"+tokens.Tokens[0].ID, nil)
	_ = res.Body.Close()

	res = do(t, c, http.MethodDelete, srv.URL+"/api/dashboards/anna-view", nil)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("delete: %d", res.StatusCode)
	}
	if len(boards.For("test")) != 1 {
		t.Error("the deleted dashboard is still in the store")
	}
	if _, err := os.Stat(filepath.Join(dir, "test", "dashboards", "anna-view.yaml")); err == nil {
		t.Error("the file is still there")
	}
}

func TestDisplayTokenCannotEditDashboards(t *testing.T) {
	srv, _, _ := dashboardFixture(t)
	c := client(t)
	_ = post(t, c, srv.URL+"/api/login", map[string]string{"name": "anna", "password": "pw"}).Body.Close()
	tok := post(t, c, srv.URL+"/api/display-tokens", map[string]string{
		"name": "tv", "platform": "test", "dashboard": "ops",
	})
	var token struct{ Secret string }
	_ = json.NewDecoder(tok.Body).Decode(&token)
	_ = tok.Body.Close()

	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/dashboards/tv-made?token="+token.Secret,
		bytes.NewReader([]byte(`{"columns":[{"panels":[{"type":"status"}]}]}`)))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("a display token could save a dashboard: %d", res.StatusCode)
	}
}

func TestAPlatformWithoutDashboardsGetsAnOverview(t *testing.T) {
	srv, _, _ := dashboardFixture(t)
	c := client(t)
	_ = post(t, c, srv.URL+"/api/login", map[string]string{"name": "anna", "password": "pw"}).Body.Close()
	list := func() []dashboard.Dashboard {
		t.Helper()
		res, err := c.Get(srv.URL + "/api/dashboards?platform=web")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		var body struct{ Dashboards []dashboard.Dashboard }
		if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		return body.Dashboards
	}

	got := list()
	if len(got) != 1 || got[0].ID != dashboard.OverviewID || !got[0].Generated || !got[0].Default {
		t.Fatalf("dashboards = %+v, want only the generated overview", got)
	}

	tok := post(t, c, srv.URL+"/api/display-tokens", map[string]string{
		"name": "tv", "platform": "web", "dashboard": dashboard.OverviewID,
	})
	var token struct{ ID string }
	_ = json.NewDecoder(tok.Body).Decode(&token)
	_ = tok.Body.Close()
	if tok.StatusCode != http.StatusCreated {
		t.Fatalf("pairing a screen with the overview: %d", tok.StatusCode)
	}

	res := do(t, c, http.MethodDelete, srv.URL+"/api/dashboards/overview?platform=web", nil)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Errorf("delete overview: %d, want 409", res.StatusCode)
	}

	mine := map[string]any{"name": "Mine", "columns": []any{map[string]any{"panels": []any{map[string]any{"type": "status"}}}}}
	res = do(t, c, http.MethodPut, srv.URL+"/api/dashboards/mine?platform=web", mine)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("put: %d", res.StatusCode)
	}
	got = list()
	if len(got) != 2 || got[1].ID != dashboard.OverviewID || got[1].Default {
		t.Fatalf("dashboards = %+v, want the screen's overview kept, no longer the default", got)
	}

	res = do(t, c, http.MethodDelete, srv.URL+"/api/display-tokens/"+token.ID, nil)
	_ = res.Body.Close()
	if got = list(); len(got) != 1 || got[0].ID != "mine" {
		t.Fatalf("dashboards = %+v, want the overview gone once nothing shows it", got)
	}
}
