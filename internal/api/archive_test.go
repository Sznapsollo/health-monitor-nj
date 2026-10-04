package api_test

import (
	"compress/gzip"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/api"
	"github.com/Sznapsollo/health-monitor-nj/internal/core"
	"github.com/Sznapsollo/health-monitor-nj/internal/signal"
	"github.com/Sznapsollo/health-monitor-nj/internal/state"
	"github.com/Sznapsollo/health-monitor-nj/internal/store"
	"github.com/prometheus/client_golang/prometheus"
)

func archiveFixture(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	reg := signal.NewRegistry()
	db, err := store.Open(store.Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	log := slog.New(slog.NewTextHandler(discard{}, nil))
	brain := core.New(state.NewStore(reg, time.Now), reg, log, time.Now)
	brain.SetLogging(core.Logging{ArchiveDir: t.TempDir(), ArchiveKeepDays: 30})
	srv := httptest.NewServer(api.Handler(api.Deps{
		Log: log, Metrics: prometheus.NewRegistry(), Registry: reg, DB: db, Core: brain, Started: now,
	}))
	t.Cleanup(srv.Close)
	return srv, db
}

func statusOf(t *testing.T, srv *httptest.Server, method, path string) int {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	return res.StatusCode
}

func TestADayCanBeArchivedDownloadedAndDeleted(t *testing.T) {
	srv, db := archiveFixture(t)
	yesterday := time.Now().UTC().AddDate(0, 0, -1)
	day := yesterday.Format("20060102")
	if err := db.WriteLogs(t.Context(), []store.LogRow{
		{Signal: "dailyLogs", TS: yesterday, Account: "acme", URL: "/a"},
		{Signal: "dailyLogs", TS: time.Now(), Account: "acme", URL: "/b"},
	}); err != nil {
		t.Fatal(err)
	}

	history := measuredHistory(t, srv)
	days, _ := history["days"].([]any)
	if len(days) != 2 {
		t.Fatalf("days = %v", days)
	}
	first, _ := days[0].(map[string]any)
	if first["today"] != true || first["bytes"].(float64) <= 0 {
		t.Errorf("today = %v, want flagged and measured", first)
	}

	if code := statusOf(t, srv, http.MethodPost, "/api/history/"+time.Now().UTC().Format("20060102")+"/archive"); code != http.StatusConflict {
		t.Errorf("archiving today gave %d", code)
	}
	if code := statusOf(t, srv, http.MethodPost, "/api/history/"+day+"/archive"); code != http.StatusOK {
		t.Fatalf("archive gave %d", code)
	}

	history = getJSON(t, srv, "/api/history")
	archives, _ := history["archives"].([]any)
	if len(archives) != 1 || len(history["days"].([]any)) != 1 {
		t.Fatalf("history = %v, want yesterday moved to one file", history)
	}
	file, _ := archives[0].(map[string]any)
	name, _ := file["file"].(string)
	if file["bytes"].(float64) <= 0 || history["archiveBytes"].(float64) != file["bytes"].(float64) {
		t.Errorf("file = %v, archiveBytes = %v", file, history["archiveBytes"])
	}

	res, err := srv.Client().Get(srv.URL + "/api/archives/" + name)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(zr)
	_ = res.Body.Close()
	if !strings.Contains(string(body), `"url":"/a"`) || strings.Count(string(body), "\n") != 1 {
		t.Errorf("archive = %q", body)
	}

	if code := statusOf(t, srv, http.MethodGet, "/api/archives/hm.db"); code != http.StatusBadRequest {
		t.Errorf("hm.db as an archive gave %d", code)
	}
	if code := statusOf(t, srv, http.MethodDelete, "/api/archives/"+name); code != http.StatusOK {
		t.Errorf("delete gave %d", code)
	}
	if left := getJSON(t, srv, "/api/history")["archives"].([]any); len(left) != 0 {
		t.Errorf("archives = %v after delete", left)
	}
}

func TestADayCanBeDeletedWithoutArchiving(t *testing.T) {
	srv, db := archiveFixture(t)
	yesterday := time.Now().UTC().AddDate(0, 0, -1)
	if err := db.WriteLogs(t.Context(), []store.LogRow{{Signal: "dailyLogs", TS: yesterday}}); err != nil {
		t.Fatal(err)
	}
	if code := statusOf(t, srv, http.MethodDelete, "/api/history/"+yesterday.Format("20060102")); code != http.StatusOK {
		t.Fatalf("delete gave %d", code)
	}
	history := getJSON(t, srv, "/api/history")
	if len(history["days"].([]any)) != 0 || len(history["archives"].([]any)) != 0 {
		t.Errorf("history = %v, want nothing left and no file", history)
	}
}

func TestActivityOutlivesTheLogsItCameFrom(t *testing.T) {
	srv, db := archiveFixture(t)
	yesterday := time.Now().UTC().AddDate(0, 0, -1)
	day := yesterday.Format("20060102")
	if err := db.WriteLogs(t.Context(), []store.LogRow{{Signal: "dailyLogs", TS: yesterday, Account: "acme", User: "anna"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ComputeActivity(t.Context(), day); err != nil {
		t.Fatal(err)
	}
	if code := statusOf(t, srv, http.MethodPost, "/api/history/"+day+"/archive"); code != http.StatusOK {
		t.Fatalf("archive gave %d", code)
	}
	days, _ := getJSON(t, srv, "/api/activity/days")["days"].([]any)
	if len(days) != 2 || days[0] != time.Now().UTC().Format("20060102") || days[1] != day {
		t.Errorf("days = %v, want today and the archived day", days)
	}
}

func TestAlertDaysCanBeArchivedOrDeletedButNotToday(t *testing.T) {
	srv, db := archiveFixture(t)
	now := time.Now().UTC()
	yesterday, older := now.AddDate(0, 0, -1), now.AddDate(0, 0, -2)
	if err := db.WriteAlerts(t.Context(), []store.AlertRow{
		{ID: "a", Platform: "test", Level: "WARN", Message: "x", Count: 1, First: yesterday, Last: yesterday},
		{ID: "b", Platform: "test", Level: "WARN", Message: "y", Count: 1, First: older, Last: older},
		{ID: "c", Platform: "test", Level: "WARN", Message: "z", Count: 1, First: now, Last: now},
	}); err != nil {
		t.Fatal(err)
	}
	if days := getJSON(t, srv, "/api/history")["alertDays"].([]any); len(days) != 3 || days[0].(map[string]any)["today"] != true {
		t.Fatalf("alert days = %v", days)
	}
	if code := statusOf(t, srv, http.MethodPost, "/api/alert-days/"+now.Format("20060102")+"/archive"); code != http.StatusConflict {
		t.Errorf("archiving today gave %d", code)
	}
	if code := statusOf(t, srv, http.MethodDelete, "/api/alert-days/"+now.Format("20060102")); code != http.StatusConflict {
		t.Errorf("deleting today gave %d", code)
	}
	if code := statusOf(t, srv, http.MethodPost, "/api/alert-days/"+yesterday.Format("20060102")+"/archive"); code != http.StatusOK {
		t.Fatalf("archive gave %d", code)
	}
	if code := statusOf(t, srv, http.MethodDelete, "/api/alert-days/"+older.Format("20060102")); code != http.StatusOK {
		t.Fatalf("delete gave %d", code)
	}
	history := getJSON(t, srv, "/api/history")
	if days := history["alertDays"].([]any); len(days) != 1 {
		t.Errorf("alert days left = %v, want only today", days)
	}
	files := history["archives"].([]any)
	if len(files) != 1 || files[0].(map[string]any)["alerts"] != true {
		t.Errorf("archives = %v, want yesterday's alerts file and nothing for the deleted day", files)
	}
}

func TestTheStorageHintListsOnlyBuiltInAndConfiguredKinds(t *testing.T) {
	srv, db := archiveFixture(t)
	if err := db.WriteLogs(t.Context(), []store.LogRow{{Signal: "apiSessionLogs", TS: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	kinds := getJSON(t, srv, "/api/history")["retention"].(map[string]any)["kinds"].([]any)
	var names []string
	for _, k := range kinds {
		names = append(names, k.(map[string]any)["kind"].(string))
	}
	if strings.Join(names, ",") != "dailyLogs,sendLogs" {
		t.Errorf("kinds = %v, want only the built-in ones", names)
	}
}
