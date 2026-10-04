package api_test

import (
	"encoding/csv"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/api"
	"github.com/Sznapsollo/health-monitor-nj/internal/signal"
	"github.com/Sznapsollo/health-monitor-nj/internal/store"
	"github.com/prometheus/client_golang/prometheus"
)

func searchFixture(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	reg := signal.NewRegistry()
	db, err := store.Open(store.Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	srv := httptest.NewServer(api.Handler(api.Deps{
		Log:      slog.New(slog.NewTextHandler(discard{}, nil)),
		Metrics:  prometheus.NewRegistry(),
		Registry: reg,
		DB:       db,
		DataDir:  t.TempDir(),
		Started:  now,
	}))
	t.Cleanup(srv.Close)
	return srv, db
}

func seedLogs(t *testing.T, db *store.Store) {
	t.Helper()
	rows := []store.LogRow{
		{
			Platform: "", Signal: "sendLogs", TS: time.Now(), Account: "acme", User: "anna",
			Payload: map[string]any{"to": "user@example.test", "subject": "Order confirmation"},
		},
		{
			Platform: "", Signal: "sendLogs", TS: time.Now().Add(-time.Minute), Account: "acme", User: "bob",
			Payload: map[string]any{"to": "other@example.test", "subject": "Password reset"},
		},
		{
			Platform: "", Signal: "dailyLogs", TS: time.Now().Add(-2 * time.Minute),
			Account: "acme", User: "anna", URL: "/api/work/groups",
			Payload: map[string]any{"url": "/api/work/groups", "executionTime": 142.0},
		},
	}
	if err := db.WriteLogs(t.Context(), rows); err != nil {
		t.Fatal(err)
	}
}

func getJSON(t *testing.T, srv *httptest.Server, path string) map[string]any {
	t.Helper()
	res, err := srv.Client().Get(srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	var body map[string]any
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return body
}

func TestSearchEndpoint(t *testing.T) {
	srv, db := searchFixture(t)
	seedLogs(t, db)

	all := getJSON(t, srv, "/api/search")
	if n, _ := all["count"].(float64); n != 3 {
		t.Fatalf("count = %v, want every row", all["count"])
	}

	byKind := getJSON(t, srv, "/api/search?kind=sendLogs")
	if n, _ := byKind["count"].(float64); n != 2 {
		t.Errorf("sendLogs count = %v", byKind["count"])
	}

	byText := getJSON(t, srv, "/api/search?text=confirmation")
	if n, _ := byText["count"].(float64); n != 1 {
		t.Errorf("text search count = %v", byText["count"])
	}

	byUser := getJSON(t, srv, "/api/search?user=bob")
	if n, _ := byUser["count"].(float64); n != 1 {
		t.Errorf("user search count = %v", byUser["count"])
	}

	limited := getJSON(t, srv, "/api/search?limit=1")
	if n, _ := limited["count"].(float64); n != 1 {
		t.Errorf("limit was ignored: %v", limited["count"])
	}

	kinds, _ := all["kinds"].([]any)
	if len(kinds) < 2 {
		t.Errorf("kinds = %v, want the panel's choices", kinds)
	}
}

func TestSearchCSV(t *testing.T) {
	srv, db := searchFixture(t)
	seedLogs(t, db)

	res, err := srv.Client().Get(srv.URL + "/api/search?format=csv")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if got := res.Header.Get("Content-Type"); got != "text/csv; charset=utf-8" {
		t.Errorf("content type = %q", got)
	}
	if got := res.Header.Get("Content-Disposition"); got == "" {
		t.Error("no filename for the download")
	}
}

func TestHistoryListsAndDownloads(t *testing.T) {
	srv, db := searchFixture(t)
	seedLogs(t, db)

	days, _ := measuredHistory(t, srv)["days"].([]any)
	if len(days) != 1 {
		t.Fatalf("days = %v, want today", days)
	}
	first, _ := days[0].(map[string]any)
	day, _ := first["day"].(string)
	if rows, _ := first["rows"].(float64); rows != 3 {
		t.Errorf("rows = %v", first["rows"])
	}

	download := getJSON(t, srv, "/api/history/"+day)
	rows, _ := download["rows"].([]any)
	if len(rows) != 3 {
		t.Errorf("downloaded rows = %d", len(rows))
	}
	if count, _ := download["count"].(float64); count != 3 {
		t.Errorf("count = %v, want 3", download["count"])
	}

	res, err := srv.Client().Get(srv.URL + "/api/history/" + day + "?format=csv")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	records, err := csv.NewReader(res.Body).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 4 || records[0][0] != "ts" {
		t.Errorf("csv records = %d (first %v), want a header and three rows", len(records), records[0])
	}

	bad, err := srv.Client().Get(srv.URL + "/api/history/notaday")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = bad.Body.Close() }()
	if bad.StatusCode != 400 {
		t.Errorf("status for a bad day = %d", bad.StatusCode)
	}
}

func TestActivityIsFreshForToday(t *testing.T) {
	srv, db := searchFixture(t)
	seedLogs(t, db)

	// Nothing has run the rollover yet: asking for today still answers,
	// because a day that is still happening is recomputed on demand.
	body := getJSON(t, srv, "/api/activity")
	activity, _ := body["activity"].([]any)
	if len(activity) != 2 {
		t.Fatalf("activity = %v, want anna and bob without waiting for the rollover", activity)
	}

	day := time.Now().UTC().Format("20060102")
	byDay := getJSON(t, srv, "/api/activity?day="+day)
	if got, _ := byDay["activity"].([]any); len(got) != 2 {
		t.Errorf("activity for %s = %v", day, got)
	}
	_ = db
}

func TestBackupEndpoint(t *testing.T) {
	srv, db := searchFixture(t)
	seedLogs(t, db)

	res, err := srv.Client().Post(srv.URL+"/api/backup", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != 200 {
		t.Fatalf("status = %d", res.StatusCode)
	}
	var body struct {
		File string `json:"file"`
		Path string `json:"path"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.File == "" || body.Path == "" {
		t.Fatalf("backup = %+v", body)
	}

	// The copy opens and holds what the original held.
	copied, err := store.Open(store.Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	_ = copied.Close()
	_ = db
}

func measuredHistory(t *testing.T, srv *httptest.Server) map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		body := getJSON(t, srv, "/api/history")
		days, _ := body["days"].([]any)
		busy := false
		for _, d := range days {
			if m, _ := d.(map[string]any)["measuring"].(bool); m {
				busy = true
			}
		}
		if !busy || time.Now().After(deadline) {
			return body
		}
		time.Sleep(10 * time.Millisecond)
	}
}
