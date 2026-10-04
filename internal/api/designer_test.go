package api_test

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/api"
	"github.com/Sznapsollo/health-monitor-nj/internal/auth"
	"github.com/Sznapsollo/health-monitor-nj/internal/core"
	"github.com/Sznapsollo/health-monitor-nj/internal/dashboard"
	"github.com/Sznapsollo/health-monitor-nj/internal/gauge"
	"github.com/Sznapsollo/health-monitor-nj/internal/protocol"
	"github.com/Sznapsollo/health-monitor-nj/internal/signal"
	"github.com/Sznapsollo/health-monitor-nj/internal/state"
	"github.com/Sznapsollo/health-monitor-nj/internal/status"
	"github.com/Sznapsollo/health-monitor-nj/internal/store"
	"github.com/prometheus/client_golang/prometheus"
)

type designerFixture struct {
	srv      *httptest.Server
	reg      *signal.Registry
	brain    *core.Core
	boards   *dashboard.Store
	gauges   *gauge.Store
	db       *store.Store
	statuses *status.Store
	dir      string
}

func newDesignerFixture(t *testing.T) designerFixture {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "test"), 0o755); err != nil {
		t.Fatal(err)
	}
	shipped := "signals:\n  requests:\n    kind: timeseries\n    input: { dims: [url] }\n"
	if err := os.WriteFile(filepath.Join(dir, "test", "signals.yaml"), []byte(shipped), 0o600); err != nil {
		t.Fatal(err)
	}
	reg := signal.NewRegistry()
	catalogues, err := signal.LoadCatalogues(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range catalogues {
		if err := reg.AddCatalogue(c); err != nil {
			t.Fatal(err)
		}
	}
	hot := state.NewStore(reg, func() time.Time { return now })
	brain := core.New(hot, reg, slog.New(slog.NewTextHandler(discard{}, nil)), func() time.Time { return now })
	boards := dashboard.NewStore()
	gauges := gauge.NewStore(func() time.Time { return now }, 0)

	db, err := store.Open(store.Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	manager, err := auth.Open(auth.Options{DB: db.DB(), Password: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	statuses, err := status.Open(db.DB(), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(api.Handler(api.Deps{
		Log:          slog.New(slog.NewTextHandler(discard{}, nil)),
		Metrics:      prometheus.NewRegistry(),
		Registry:     reg,
		Store:        hot,
		Core:         brain,
		Dashboards:   boards,
		Gauges:       gauges,
		Auth:         manager,
		DB:           db,
		Statuses:     statuses,
		PlatformsDir: dir,
		Started:      now,
	}))
	t.Cleanup(srv.Close)
	return designerFixture{srv: srv, reg: reg, brain: brain, boards: boards, gauges: gauges, db: db, statuses: statuses, dir: dir}
}

func unknownMetric(platform, sig, region string, ms float64) protocol.Packet {
	header := protocol.Header{V: 1, T: protocol.TypeMetric, Platform: platform, TS: protocol.NewTimestamp(now)}
	return protocol.Packet{Header: header, Metric: &protocol.Metric{
		Header: header, Signal: sig,
		Dims:   map[string]string{"region": region, "url": "/pay"},
		Values: map[string]float64{"count": 1, "ms": ms},
	}}
}

func candidatesOf(t *testing.T, c *http.Client, url string) []core.Candidate {
	t.Helper()
	res := do(t, c, http.MethodGet, url+"/api/signals/candidates", nil)
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("candidates: %d", res.StatusCode)
	}
	var body struct {
		Candidates []core.Candidate `json:"candidates"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body.Candidates
}

var checkoutEdit = map[string]any{
	"kind":        "timeseries",
	"displayName": "Checkout",
	"dims":        []any{map[string]any{"name": "region", "displayName": "Region"}, map[string]any{"name": "url"}},
	"values":      map[string]string{"count": "count", "ms": "ms"},
	"groupTop":    10,
}

func TestDesignerDefinesAQuarantinedSignal(t *testing.T) {
	f := newDesignerFixture(t)
	c := client(t)
	_ = post(t, c, f.srv.URL+"/api/login", map[string]string{"name": "anna", "password": "pw"}).Body.Close()

	f.brain.Quarantine([]protocol.Packet{
		unknownMetric("demo", "checkout", "eu", 20),
		unknownMetric("demo", "checkout", "us", 400),
		unknownMetric("demo", "checkout", "eu", 30),
	}, "unknown_signal")

	cands := candidatesOf(t, c, f.srv.URL)
	if len(cands) != 1 {
		t.Fatalf("candidates = %+v", cands)
	}
	got := cands[0]
	if got.Platform != "demo" || got.Signal != "checkout" || got.Count != 3 ||
		len(got.Dims["region"]) != 2 || got.Values["ms"] != (core.Range{Min: 20, Max: 400}) ||
		len(got.Minutes) != 1 || got.Minutes[0].Packets != 3 || got.Minutes[0].Sums["ms"] != 450 {
		t.Errorf("candidate = %+v", got)
	}

	res := do(t, c, http.MethodPut, f.srv.URL+"/api/signals/checkout?platform=demo", checkoutEdit)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("put: %d", res.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(f.dir, "demo", signal.DesignedDir, "checkout.yaml")); err != nil {
		t.Fatal(err)
	}
	def, ok := f.reg.Lookup("demo", "checkout")
	if !ok || def.ReadOnly || def.CreatedBy != "anna" || def.Values.MS != "ms" ||
		def.Display.DimName("region") != "Region" || len(def.Views) != 2 || def.Views[1].Top != 10 {
		t.Fatalf("registered = %+v", def)
	}
	if left := candidatesOf(t, c, f.srv.URL); len(left) != 0 {
		t.Errorf("still a candidate after being defined: %+v", left)
	}

	other := client(t)
	_ = post(t, other, f.srv.URL+"/api/login", map[string]string{"name": "bob", "password": "pw"}).Body.Close()
	res = do(t, other, http.MethodPut, f.srv.URL+"/api/signals/checkout?platform=demo", checkoutEdit)
	_ = res.Body.Close()
	if def, _ := f.reg.Lookup("demo", "checkout"); def.CreatedBy != "anna" || def.UpdatedBy != "bob" {
		t.Errorf("after bob's edit: created %q, updated %q", def.CreatedBy, def.UpdatedBy)
	}
}

func TestDesignerRefusals(t *testing.T) {
	f := newDesignerFixture(t)
	c := client(t)
	_ = post(t, c, f.srv.URL+"/api/login", map[string]string{"name": "anna", "password": "pw"}).Body.Close()

	cases := []struct {
		name, url string
		body      any
		want      int
	}{
		{"shipped", "/api/signals/requests?platform=test", checkoutEdit, http.StatusConflict},
		{"unknown platform", "/api/signals/checkout?platform=nowhere", checkoutEdit, http.StatusBadRequest},
		{"unsafe platform", "/api/signals/checkout?platform=..", checkoutEdit, http.StatusBadRequest},
		{"unsafe name", "/api/signals/.hidden?platform=test", checkoutEdit, http.StatusBadRequest},
		{"kind", "/api/signals/audit?platform=test", map[string]any{"kind": "status"}, http.StatusBadRequest},
		{"duplicate dim", "/api/signals/x?platform=test", map[string]any{
			"kind": "timeseries", "dims": []any{map[string]any{"name": "a"}, map[string]any{"name": "a"}},
		}, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := do(t, c, http.MethodPut, f.srv.URL+tc.url, tc.body)
			_ = res.Body.Close()
			if res.StatusCode != tc.want {
				t.Errorf("got %d, want %d", res.StatusCode, tc.want)
			}
		})
	}
	if _, err := os.Stat(filepath.Join(f.dir, "test", signal.DesignedDir)); !os.IsNotExist(err) {
		t.Errorf("a refused save left files behind: %v", err)
	}

	anonymous := client(t)
	res := do(t, anonymous, http.MethodPut, f.srv.URL+"/api/signals/x?platform=test", checkoutEdit)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("without a login: %d", res.StatusCode)
	}
}

func TestDesignerDelete(t *testing.T) {
	f := newDesignerFixture(t)
	c := client(t)
	_ = post(t, c, f.srv.URL+"/api/login", map[string]string{"name": "anna", "password": "pw"}).Body.Close()

	for _, name := range []string{"checkout", "refunds"} {
		res := do(t, c, http.MethodPut, f.srv.URL+"/api/signals/"+name+"?platform=test", checkoutEdit)
		_ = res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("put %s: %d", name, res.StatusCode)
		}
	}
	f.boards.Replace("test", []dashboard.Dashboard{{
		ID: "sales", Platform: "test", Name: "Sales",
		Rows: []dashboard.Row{{Columns: []dashboard.Column{{Panels: []dashboard.Panel{
			{Type: dashboard.PanelChart, Signal: "checkout"},
		}}}}},
	}})

	for _, tc := range []struct {
		name string
		want int
	}{
		{"requests", http.StatusConflict},
		{"checkout", http.StatusConflict},
		{"refunds", http.StatusOK},
	} {
		res := do(t, c, http.MethodDelete, f.srv.URL+"/api/signals/"+tc.name+"?platform=test", nil)
		_ = res.Body.Close()
		if res.StatusCode != tc.want {
			t.Errorf("delete %s: %d, want %d", tc.name, res.StatusCode, tc.want)
		}
	}
	if _, ok := f.reg.Lookup("test", "refunds"); ok {
		t.Error("refunds is still registered")
	}
	if _, ok := f.reg.Lookup("test", "checkout"); !ok {
		t.Error("checkout went although a dashboard charts it")
	}
}

func TestAGaugeShowsOnlyOnceDefined(t *testing.T) {
	f := newDesignerFixture(t)
	c := client(t)
	_ = post(t, c, f.srv.URL+"/api/login", map[string]string{"name": "anna", "password": "pw"}).Body.Close()
	f.gauges.Observe("test", "vpnUsage", "gw-1", []gauge.Point{{Label: "anna", Value: 512}}, now)

	shown := func() int {
		res := do(t, c, http.MethodGet, f.srv.URL+"/api/gauges?platform=test", nil)
		defer func() { _ = res.Body.Close() }()
		var body struct {
			Gauges []gauge.View `json:"gauges"`
		}
		if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		return len(body.Gauges)
	}
	if n := shown(); n != 0 {
		t.Fatalf("an undefined gauge is shown: %d", n)
	}
	cands := candidatesOf(t, c, f.srv.URL)
	if len(cands) != 1 || cands[0].Signal != "vpnUsage" || cands[0].Kind != "gauge" {
		t.Fatalf("candidates = %+v", cands)
	}

	res := do(t, c, http.MethodPut, f.srv.URL+"/api/signals/vpnUsage?platform=test", map[string]any{"kind": "gauge"})
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("put: %d", res.StatusCode)
	}
	if n := shown(); n != 1 {
		t.Errorf("the defined gauge is not shown: %d", n)
	}
}

func TestALogKindCanBeDefinedWithItsOwnRetention(t *testing.T) {
	f := newDesignerFixture(t)
	c := client(t)
	_ = post(t, c, f.srv.URL+"/api/login", map[string]string{"name": "anna", "password": "pw"}).Body.Close()
	f.brain.SetLogging(core.Logging{KeepDays: 14, ArchiveDir: t.TempDir(), ArchiveKeepDays: 30})
	if err := f.db.WriteLogs(t.Context(), []store.LogRow{
		{Platform: "test", Signal: "apiSessionLogs", TS: now},
		{Platform: "test", Signal: "dailyLogs", TS: now},
		{Platform: "test", Signal: "sendLogs", TS: now},
	}); err != nil {
		t.Fatal(err)
	}

	res := do(t, c, http.MethodGet, f.srv.URL+"/api/signals/candidates?platform=test", nil)
	var body struct{ Candidates []core.Candidate }
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if len(body.Candidates) != 1 || body.Candidates[0].Signal != "apiSessionLogs" || body.Candidates[0].Kind != "log" {
		t.Fatalf("candidates = %+v, want only the custom log kind, not the monitor's own", body.Candidates)
	}

	res = do(t, c, http.MethodPut, f.srv.URL+"/api/signals/apiSessionLogs?platform=test", map[string]any{
		"kind": "log", "displayName": "API sessions", "retention": map[string]any{"logDays": 5, "archiveDays": 60},
	})
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("put: %d", res.StatusCode)
	}
	file, err := os.ReadFile(filepath.Join(f.dir, "test", signal.DesignedDir, "apiSessionLogs.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"kind: log", "log_days: 5", "archive_days: 60", "name: API sessions", "created_by: anna"} {
		if !strings.Contains(string(file), want) {
			t.Errorf("file lacks %q:\n%s", want, file)
		}
	}
	if strings.Contains(string(file), "views") || strings.Contains(string(file), "hot_detail") {
		t.Errorf("a log signal was saved with chart settings:\n%s", file)
	}
	if left := candidatesOf(t, c, f.srv.URL); len(left) != 0 {
		t.Errorf("still waiting after being defined: %+v", left)
	}

	_, kinds := f.brain.LogRetention([]string{"apiSessionLogs"})
	var got core.Retention
	for _, k := range kinds {
		if k.Kind == "apiSessionLogs" {
			got = k
		}
	}
	if got != (core.Retention{Kind: "apiSessionLogs", Name: "API sessions", DBDays: 5, ArchiveDays: 60}) {
		t.Errorf("retention = %+v, want the signal's own days over the configuration", got)
	}
}

func TestASignalSentByMistakeCanBeRemoved(t *testing.T) {
	f := newDesignerFixture(t)
	c := client(t)
	_ = post(t, c, f.srv.URL+"/api/login", map[string]string{"name": "anna", "password": "pw"}).Body.Close()
	f.brain.SetLogging(core.Logging{KeepDays: 14})
	f.brain.Quarantine([]protocol.Packet{unknownMetric("test", "refunds", "eu", 40)}, "unknown_signal")
	f.gauges.Observe("test", "vpnUsage", "gw-1", []gauge.Point{{Label: "anna", Value: 512}}, now)
	if err := f.db.WriteLogs(t.Context(), []store.LogRow{
		{Platform: "test", Signal: "typoLogs", TS: now},
		{Platform: "test", Signal: "typoLogs", TS: now.Add(-24 * time.Hour)},
		{Platform: "test", Signal: "dailyLogs", TS: now},
	}); err != nil {
		t.Fatal(err)
	}
	if got := len(candidatesOf(t, c, f.srv.URL)); got != 3 {
		t.Fatalf("candidates = %d, want refunds, vpnUsage and typoLogs", got)
	}
	for _, tc := range []struct{ name, kind string }{{"refunds", "timeseries"}, {"vpnUsage", "gauge"}, {"typoLogs", "log"}} {
		res := do(t, c, http.MethodGet, f.srv.URL+"/api/signals/candidates/"+tc.name+"/sample?platform=test&kind="+tc.kind, nil)
		_ = res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Errorf("sample of %s: %d", tc.name, res.StatusCode)
		}
	}

	for _, tc := range []struct{ name, kind string }{{"refunds", "timeseries"}, {"vpnUsage", "gauge"}, {"typoLogs", "log"}} {
		res := do(t, c, http.MethodDelete, f.srv.URL+"/api/signals/candidates/"+tc.name+"?platform=test&kind="+tc.kind, nil)
		_ = res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("remove %s: %d", tc.name, res.StatusCode)
		}
	}
	if left := candidatesOf(t, c, f.srv.URL); len(left) != 0 {
		t.Fatalf("candidates = %+v, want none", left)
	}
	for _, g := range f.brain.QuarantineSnapshot() {
		if g.Signal == "refunds" {
			t.Errorf("refunds is still among the packets that could not be placed")
		}
	}
	tables, _ := f.db.LogTables(t.Context())
	for _, tb := range tables {
		if tb.Signal == "typoLogs" {
			t.Errorf("a day of typoLogs is still stored: %s", tb.Day)
		}
	}

	for _, tc := range []struct {
		path string
		want int
	}{
		{"dailyLogs?platform=test&kind=log", http.StatusBadRequest},
		{"requests?platform=test&kind=timeseries", http.StatusConflict},
	} {
		res := do(t, c, http.MethodDelete, f.srv.URL+"/api/signals/candidates/"+tc.path, nil)
		_ = res.Body.Close()
		if res.StatusCode != tc.want {
			t.Errorf("remove %s: %d, want %d", tc.path, res.StatusCode, tc.want)
		}
	}
}

func TestAnUnknownReportTypeWaitsToBeDefinedAsInfo(t *testing.T) {
	f := newDesignerFixture(t)
	c := client(t)
	_ = post(t, c, f.srv.URL+"/api/login", map[string]string{"name": "anna", "password": "pw"}).Body.Close()
	report := []byte(`{"type":"shopModulesReport","serverName":"soak-1","moduleStatusMap":{"cart":{"heartBeat":1}}}`)
	f.brain.NoteUnknownType("test", "shopModulesReport", report)
	f.brain.NoteUnknownType("test", "shopModulesReport", report)

	cands := candidatesOf(t, c, f.srv.URL)
	if len(cands) != 1 || cands[0].Signal != "shopModulesReport" || cands[0].Kind != "info" || cands[0].Count != 2 {
		t.Fatalf("candidates = %+v, want the report type waiting as info", cands)
	}
	if _, ok := cands[0].Dims["moduleStatusMap"]; !ok {
		t.Errorf("fields = %v, want the report's own", cands[0].Dims)
	}

	res0 := do(t, c, http.MethodGet, f.srv.URL+"/api/signals/candidates/shopModulesReport/sample?platform=test&kind=info", nil)
	var shown struct {
		Sample map[string]any `json:"sample"`
	}
	if err := json.NewDecoder(res0.Body).Decode(&shown); err != nil {
		t.Fatal(err)
	}
	_ = res0.Body.Close()
	if shown.Sample["serverName"] != "soak-1" || shown.Sample["moduleStatusMap"] == nil {
		t.Fatalf("sample = %v, want the packet as it arrived", shown.Sample)
	}

	res := do(t, c, http.MethodPut, f.srv.URL+"/api/signals/shopModules?platform=test", map[string]any{
		"kind": "info", "packetType": "shopModulesReport", "merge": true,
	})
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("put: %d", res.StatusCode)
	}
	if left := candidatesOf(t, c, f.srv.URL); len(left) != 0 {
		t.Fatalf("candidates = %+v, want none once an info signal names the type", left)
	}
}

func TestAnInfoSignalTakenOffStatusDropsItsRows(t *testing.T) {
	f := newDesignerFixture(t)
	c := client(t)
	_ = post(t, c, f.srv.URL+"/api/login", map[string]string{"name": "anna", "password": "pw"}).Body.Close()
	f.statuses.Observe("test", "dailyReport", "app-1", nil, now)
	f.statuses.Observe("test", "servers", "app-1", nil, now)

	res := do(t, c, http.MethodPut, f.srv.URL+"/api/signals/dailyReport?platform=test", map[string]any{
		"kind": "info", "packetType": "godzinkiDailyReportStatus", "noStatus": true,
	})
	var saved struct {
		NoStatus bool `json:"noStatus"`
	}
	if err := json.NewDecoder(res.Body).Decode(&saved); err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || !saved.NoStatus {
		t.Fatalf("put: %d, noStatus %v", res.StatusCode, saved.NoStatus)
	}
	if list := f.statuses.List("test"); len(list) != 1 || list[0].Signal != "servers" {
		t.Fatalf("status = %+v, want only the servers row", list)
	}
}

func TestTheEditorIsToldWhatAnEmptyLogRetentionMeans(t *testing.T) {
	f := newDesignerFixture(t)
	c := client(t)
	_ = post(t, c, f.srv.URL+"/api/login", map[string]string{"name": "anna", "password": "pw"}).Body.Close()
	f.brain.SetLogging(core.Logging{
		KeepDays: 1, KeepDaysByKind: map[string]int{"dailyLogs": 2},
		ArchiveDir: t.TempDir(), ArchiveKeepDays: 2,
	})
	for kind, want := range map[string]int{"loginLogs": 1, "dailyLogs": 2} {
		res := do(t, c, http.MethodGet, f.srv.URL+"/api/logs/retention?kind="+kind, nil)
		var got core.Retention
		if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if got.DBDays != want || got.ArchiveDays != 2 {
			t.Errorf("%s = %+v, want %d days in the database and 2 as files", kind, got, want)
		}
	}
}

func TestAnUnknownReportTypeCanBeChartedFromItsFields(t *testing.T) {
	f := newDesignerFixture(t)
	c := client(t)
	_ = post(t, c, f.srv.URL+"/api/login", map[string]string{"name": "anna", "password": "pw"}).Body.Close()
	f.brain.NoteUnknownType("test", "ksefEventStatusData",
		[]byte(`{"type":"ksefEventStatusData","taskStatus":"COMPLETED","itemsCount":3,"executionTime":1}`))

	cands := candidatesOf(t, c, f.srv.URL)
	if len(cands) != 1 || cands[0].Values["itemsCount"].Max != 3 || len(cands[0].Dims["taskStatus"]) != 1 {
		t.Fatalf("candidates = %+v, want its fields and values gathered", cands)
	}

	res := do(t, c, http.MethodPut, f.srv.URL+"/api/signals/ksefEvents?platform=test", map[string]any{
		"kind": "timeseries", "packetType": "ksefEventStatusData",
		"dims":   []map[string]string{{"name": "taskStatus"}},
		"values": map[string]string{"count": "itemsCount", "ms": "executionTime"},
	})
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("put: %d", res.StatusCode)
	}
	if got := f.reg.MetricFor("test", "ksefEventStatusData"); got != "ksefEvents" {
		t.Fatalf("MetricFor = %q, want the saved chart", got)
	}
	if left := candidatesOf(t, c, f.srv.URL); len(left) != 0 {
		t.Fatalf("candidates = %+v, want none once a chart names the type", left)
	}
}
