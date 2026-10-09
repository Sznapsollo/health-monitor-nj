// Package api serves the HTTP surface: health, metrics and the embedded SPA.
package api

import (
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/alert"
	"github.com/Sznapsollo/health-monitor-nj/internal/auth"
	"github.com/Sznapsollo/health-monitor-nj/internal/buildinfo"
	"github.com/Sznapsollo/health-monitor-nj/internal/core"
	"github.com/Sznapsollo/health-monitor-nj/internal/dashboard"
	"github.com/Sznapsollo/health-monitor-nj/internal/gauge"
	"github.com/Sznapsollo/health-monitor-nj/internal/health"
	"github.com/Sznapsollo/health-monitor-nj/internal/hub"
	"github.com/Sznapsollo/health-monitor-nj/internal/info"
	"github.com/Sznapsollo/health-monitor-nj/internal/intake"
	hmsignal "github.com/Sznapsollo/health-monitor-nj/internal/signal"
	"github.com/Sznapsollo/health-monitor-nj/internal/silence"
	"github.com/Sznapsollo/health-monitor-nj/internal/state"
	"github.com/Sznapsollo/health-monitor-nj/internal/status"
	"github.com/Sznapsollo/health-monitor-nj/internal/store"
	"github.com/Sznapsollo/health-monitor-nj/internal/sysstats"
	"github.com/Sznapsollo/health-monitor-nj/internal/visits"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Deps are everything the HTTP layer needs from the rest of the server.
type Deps struct {
	Log       *slog.Logger
	Metrics   *prometheus.Registry
	Assets    fs.FS
	HasAssets bool
	Started   time.Time

	// Registry, Store, Core, Hub and Intake are optional: without them the
	// server still serves health, metrics and the SPA.
	Registry *hmsignal.Registry
	Store    *state.Store
	Core     *core.Core
	Hub      *hub.Hub
	Intake   intakeCounters
	DB       *store.Store
	Writer   *store.Writer
	Alerts   *alert.Store
	Silences *silence.Store
	Statuses *status.Store
	Health   *health.Book
	System   *sysstats.Sampler
	// PlatformsDir is where rules are saved; empty makes them read-only.
	PlatformsDir string
	// DataDir is where backups are written; empty disables the backup action.
	DataDir string
	// LogWriter is exposed so the state endpoint can report what it is doing.
	LogWriter *store.LogWriter
	// AlertWriter is the same for the alert history, and its dropped count is
	// worth watching: a gap there is a gap in the record.
	AlertWriter *store.AlertWriter
	// Auth checks who is asking. Nil leaves everything open, which is only
	// right for a development server.
	Auth *auth.Manager
	// Gauges holds the latest labelled values a platform reports.
	Gauges *gauge.Store
	// Visits is the viewing history; nil keeps none.
	Visits *visits.Tracker
	// Info keeps the reports of info signals; nil keeps none.
	Info *info.Store
	// Dashboards are the arrangements each platform defines.
	Dashboards *dashboard.Store
	// Tap shows the datagrams received while someone watches; nil shows none.
	Tap *intake.Tap

	// WSOrigins are the browser origins allowed to open a WebSocket. Empty
	// means same-origin only, which is what production uses.
	WSOrigins []string

	logins *loginLimiter
}

// intakeCounters is the little the API needs from the intake, kept as an
// interface so the HTTP layer does not depend on the whole listener.
type intakeCounters interface {
	Counters() intake.Counters
}

// Handler builds the public HTTP handler.
func Handler(d Deps) http.Handler {
	d.logins = newLoginLimiter(time.Now)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"status":        "ok",
			"version":       buildinfo.Version,
			"commit":        buildinfo.Commit,
			"uptimeSeconds": int(time.Since(d.Started).Seconds()),
			"serverTime":    time.Now().UTC().Format(time.RFC3339Nano),
		})
	})
	mux.HandleFunc("GET /api/version", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"version":   buildinfo.Version,
			"commit":    buildinfo.Commit,
			"goVersion": buildinfo.GoVersion(),
		})
	})
	mux.Handle("GET /api/metrics", promhttp.HandlerFor(d.Metrics, promhttp.HandlerOpts{}))
	mux.HandleFunc("GET /api/catalogue", d.handleCatalogue)
	mux.HandleFunc("GET /api/series", d.handleSeries)
	mux.HandleFunc("GET /api/state", d.handleState)
	mux.HandleFunc("GET /api/health", d.handleHealth)
	mux.HandleFunc("GET /api/server", d.handleServer)
	mux.HandleFunc("GET /api/packets", d.handlePackets)
	mux.HandleFunc("POST /api/health/known", d.handleMarkKnown)
	mux.HandleFunc("GET /api/sessions", d.handleSessions)
	mux.HandleFunc("GET /api/visits", d.handleVisits)
	mux.HandleFunc("GET /api/info", d.handleInfo)
	mux.HandleFunc("GET /api/info/report", d.handleInfoReport)
	mux.HandleFunc("DELETE /api/info/report", d.handleForgetInfo)
	mux.HandleFunc("GET /api/visits/days", d.handleVisitDays)
	mux.HandleFunc("GET /api/alerts", d.handleAlerts)
	mux.HandleFunc("GET /api/status", d.handleStatus)
	mux.HandleFunc("POST /api/status/forget", d.handleForget)
	mux.HandleFunc("GET /api/silences", d.handleSilences)
	mux.HandleFunc("POST /api/silences", d.handleCreateSilence)
	mux.HandleFunc("DELETE /api/silences/{id}", d.handleDeleteSilence)
	mux.HandleFunc("PUT /api/silences/{id}", d.handleUpdateSilence)
	mux.HandleFunc("GET /api/rules", d.handleGetRules)
	mux.HandleFunc("PUT /api/rules", d.handlePutRules)
	mux.HandleFunc("POST /api/rules/test", d.handleTestRules)
	mux.HandleFunc("GET /api/search", d.handleSearch)
	mux.HandleFunc("GET /api/history", d.handleHistory)
	mux.HandleFunc("GET /api/history/{day}", d.handleHistoryDay)
	mux.HandleFunc("POST /api/history/{day}/archive", d.handleArchiveDay)
	mux.HandleFunc("DELETE /api/history/{day}", d.handleDeleteDay)
	mux.HandleFunc("POST /api/alert-days/{day}/archive", d.handleArchiveAlertDay)
	mux.HandleFunc("DELETE /api/alert-days/{day}", d.handleDeleteAlertDay)
	mux.HandleFunc("GET /api/archives/{file}", d.handleArchiveFile)
	mux.HandleFunc("DELETE /api/archives/{file}", d.handleDeleteArchive)
	mux.HandleFunc("POST /api/storage/reclaim", d.handleReclaim)
	mux.HandleFunc("GET /api/activity", d.handleActivity)
	mux.HandleFunc("GET /api/activity/days", d.handleActivityDays)
	mux.HandleFunc("POST /api/backup", d.handleBackup)
	mux.HandleFunc("GET /api/backups", d.handleBackups)
	mux.HandleFunc("GET /api/backups/{file}", d.handleBackupFile)
	mux.HandleFunc("DELETE /api/backups/{file}", d.handleDeleteBackup)
	mux.HandleFunc("POST /api/login", d.handleLogin)
	mux.HandleFunc("POST /api/logout", d.handleLogout)
	mux.HandleFunc("GET /api/session", d.handleSession)
	mux.HandleFunc("GET /api/display-tokens", d.handleDisplayTokens)
	mux.HandleFunc("POST /api/display-tokens", d.handleCreateDisplayToken)
	mux.HandleFunc("DELETE /api/display-tokens/{id}", d.handleRevokeDisplayToken)
	mux.HandleFunc("PUT /api/display-tokens/{id}", d.handleDisplayOptions)
	mux.HandleFunc("POST /api/message", d.handleMessage)
	mux.HandleFunc("GET /api/gauges", d.handleGauges)
	mux.HandleFunc("GET /api/dashboards", d.handleDashboards)
	mux.HandleFunc("GET /api/signals/candidates", d.handleSignalCandidates)
	mux.HandleFunc("GET /api/signals/export", d.handleExportSignals)
	mux.HandleFunc("POST /api/signals/import", d.handleImportSignals)
	mux.HandleFunc("GET /api/logs/retention", d.handleLogRetention)
	mux.HandleFunc("DELETE /api/signals/candidates/{name}", d.handleDismissCandidate)
	mux.HandleFunc("GET /api/signals/candidates/{name}/sample", d.handleCandidateSample)
	mux.HandleFunc("PUT /api/signals/{name}", d.handlePutSignal)
	mux.HandleFunc("DELETE /api/signals/{name}", d.handleDeleteSignal)
	mux.HandleFunc("PUT /api/dashboards/{id}", d.handlePutDashboard)
	mux.HandleFunc("DELETE /api/dashboards/{id}", d.handleDeleteDashboard)
	mux.HandleFunc("/ws", d.handleWS)
	mux.Handle("/", spa(d))
	return logRequests(d.Log, d.authenticate(d.guardWrites(mux)))
}

// AdminHandler builds the private handler: pprof, kept off the public port.
func AdminHandler() http.Handler {
	mux := http.NewServeMux()
	registerPprof(mux)
	return mux
}

// spa serves the embedded build, falling back to index.html so client-side
// routes survive a reload.
func spa(d Deps) http.Handler {
	if !d.HasAssets {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusNotFound, map[string]any{
				"error": "no SPA build embedded; run `make web-build`, or use the Vite dev server on :5173 during development",
			})
		})
	}
	files := http.FileServerFS(d.Assets)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only a page is ever served from here. Without this, an endpoint the
		// server does not have — an old build meeting a new browser — answers
		// a POST or a DELETE with 200 and index.html, and the caller reads
		// that as "done".
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{
				"error": r.Method + " " + r.URL.Path + " is not something this server does",
			})
			return
		}
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}
		if _, err := fs.Stat(d.Assets, name); err != nil {
			r = r.Clone(r.Context())
			r.URL.Path = "/"
		}
		files.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// Unwrap lets http.ResponseController reach the real writer, which is what a
// WebSocket upgrade needs to hijack the connection. Without it every upgrade
// through this middleware fails with 501.
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

func logRequests(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		log.Debug("http request",
			"method", r.Method, "path", r.URL.Path,
			"status", rec.status, "duration_ms", time.Since(start).Milliseconds())
	})
}
