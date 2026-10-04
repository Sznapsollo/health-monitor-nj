// Command hm is the healthMonitorNJ server: UDP intake, HTTP API and the
// embedded single-page application in one static binary.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"sync"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/Sznapsollo/health-monitor-nj/internal/adapter/legacy"
	"github.com/Sznapsollo/health-monitor-nj/internal/alert"
	"github.com/Sznapsollo/health-monitor-nj/internal/api"
	"github.com/Sznapsollo/health-monitor-nj/internal/auth"
	"github.com/Sznapsollo/health-monitor-nj/internal/buildinfo"
	"github.com/Sznapsollo/health-monitor-nj/internal/config"
	"github.com/Sznapsollo/health-monitor-nj/internal/core"
	"github.com/Sznapsollo/health-monitor-nj/internal/dashboard"
	"github.com/Sznapsollo/health-monitor-nj/internal/gauge"
	"github.com/Sznapsollo/health-monitor-nj/internal/health"
	"github.com/Sznapsollo/health-monitor-nj/internal/hub"
	"github.com/Sznapsollo/health-monitor-nj/internal/info"
	"github.com/Sznapsollo/health-monitor-nj/internal/intake"
	"github.com/Sznapsollo/health-monitor-nj/internal/obs"
	"github.com/Sznapsollo/health-monitor-nj/internal/protocol"
	"github.com/Sznapsollo/health-monitor-nj/internal/rules"
	"github.com/Sznapsollo/health-monitor-nj/internal/seed"
	// Aliased: os/signal already owns the name `signal` in this file.
	hmsignal "github.com/Sznapsollo/health-monitor-nj/internal/signal"
	"github.com/Sznapsollo/health-monitor-nj/internal/silence"
	"github.com/Sznapsollo/health-monitor-nj/internal/state"
	"github.com/Sznapsollo/health-monitor-nj/internal/status"
	"github.com/Sznapsollo/health-monitor-nj/internal/store"
	"github.com/Sznapsollo/health-monitor-nj/internal/sysstats"
	"github.com/Sznapsollo/health-monitor-nj/internal/visits"
	"github.com/Sznapsollo/health-monitor-nj/web"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

const shutdownGrace = 10 * time.Second

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "hm:", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "config.yaml", "path to config.yaml")
	showVersion := flag.Bool("version", false, "print the version and exit")
	healthcheck := flag.Bool("healthcheck", false, "exit 0 when this config's server answers /healthz; for container healthchecks")
	flag.Parse()

	if *showVersion {
		fmt.Printf("hm %s (%s, %s)\n", buildinfo.Version, buildinfo.Commit, buildinfo.GoVersion())
		return nil
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if *healthcheck {
		return checkHealth(cfg.Server.HTTPAddr)
	}
	log, err := obs.NewLogger(cfg.Log.Level, cfg.Log.Format)
	if err != nil {
		return err
	}
	if from := cfg.Data.SeedPlatformsFrom; from != "" {
		copied, err := seed.Missing(from, cfg.Data.PlatformsDir)
		if err != nil {
			return fmt.Errorf("seeding %s from %s: %w", cfg.Data.PlatformsDir, from, err)
		}
		if len(copied) > 0 {
			log.Info("platform files seeded", "from", from, "to", cfg.Data.PlatformsDir, "files", copied)
		}
	}

	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	metrics := obs.NewMetrics(reg)
	metrics.BuildInfo.WithLabelValues(buildinfo.Version, buildinfo.Commit, buildinfo.GoVersion()).Set(1)

	assets, hasAssets := web.Assets()
	if !hasAssets {
		log.Warn("no SPA build embedded; serving the API only")
	}

	registry := hmsignal.NewRegistry()
	catalogues, err := hmsignal.LoadCatalogues(cfg.Data.PlatformsDir)
	if err != nil {
		return err
	}
	for _, cat := range catalogues {
		if err := registry.AddCatalogue(cat); err != nil {
			return err
		}
		log.Info("platform catalogue loaded", "platform", cat.Platform, "signals", len(cat.Signals), "file", cat.Source)
	}
	if len(catalogues) == 0 {
		log.Warn("no platform catalogue found; unknown signals will be quarantined",
			"dir", cfg.Data.PlatformsDir)
	}

	hot := state.NewStore(registry, time.Now)
	containerLimit := sysstats.MemoryLimit()
	hotLimit, hotSource := cfg.State.HotLimit(containerLimit)
	hot.SetMaxHotBytes(hotLimit)
	log.Info("hot state memory guard", "limitMB", hotLimit>>20, "source", hotSource)
	if containerLimit > 0 && containerLimit < config.SmallContainerBytes {
		log.Warn("the container memory limit leaves little room for the charts' in-memory detail; 512 MB or more is recommended",
			"limitMB", containerLimit>>20)
	}
	// The collector otherwise lets the heap reach twice the live data, which
	// inside a container limit is the difference between fitting and being
	// killed; an explicit GOMEMLIMIT still wins.
	if containerLimit > 0 && os.Getenv("GOMEMLIMIT") == "" {
		debug.SetMemoryLimit(containerLimit / 10 * 9)
		log.Info("go memory limit set from the container", "limitMB", containerLimit/10*9>>20)
	}
	brain := core.New(hot, registry, log, time.Now)

	// Aggregates outlive the process: SQLite under the data directory, with a
	// batched writer that never blocks the readers.
	if err := os.MkdirAll(cfg.Data.Dir, 0o750); err != nil {
		return fmt.Errorf("data dir: %w", err)
	}
	db, err := store.Open(store.Options{Dir: cfg.Data.Dir, Log: log})
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	writer := store.NewWriter(db, store.WriterOptions{
		FlushEvery: cfg.Data.AggregateFlushEvery,
		Log:        log,
		Layout: func(platform, sig string) (store.Layout, bool) {
			def, ok := registry.Lookup(platform, sig)
			if !ok || def.Kind != hmsignal.KindTimeseries {
				return store.Layout{}, false
			}
			return store.Layout{Dims: def.Dims, Pairs: hot.PairsOf(platform, sig), MaxKeys: def.MaxKeys, Labels: def.Display.Labels}, true
		},
	})
	defer writer.Stop()
	brain.SetDurable(writer)

	logWriter := store.NewLogWriter(db, store.WriterOptions{
		FlushEvery: cfg.Data.FlushEvery,
		Log:        log,
	})
	defer logWriter.Stop()

	alertWriter := store.NewAlertWriter(db, store.WriterOptions{
		FlushEvery: cfg.Data.FlushEvery,
		Log:        log,
	})
	defer alertWriter.Stop()

	brain.SetDiskWatch(core.DiskWatch{
		Folders: map[string]string{
			"database": cfg.Data.Dir, "archive": filepath.Join(cfg.Data.Dir, "archive"), "backups": cfg.Data.Backups(),
		},
		MinFree: int64(cfg.Data.MinFreeGB * (1 << 30)),
	})
	brain.SetLogging(core.Logging{
		Writer:                logWriter,
		KeepDays:              cfg.Data.LogKeepDays,
		KeepDaysByKind:        cfg.Data.LogKeepDaysByKind,
		ArchiveDir:            filepath.Join(cfg.Data.Dir, "archive"),
		ArchiveKeepDays:       cfg.Data.LogArchiveKeepDays,
		ArchiveKeepDaysByKind: cfg.Data.LogArchiveKeepDaysByKind,
		ActivityDays:          cfg.Data.ActivityKeepDays,
		AlertDays:             cfg.Data.AlertKeepDays,
		AlertArchiveDays:      cfg.Data.AlertArchiveKeepDays,
	})

	// Logs used to be one table a day holding every kind. Those cannot take
	// per-kind retention, and nothing writes to them any more.
	if n, err := db.DropLegacyLogTables(context.Background()); err != nil {
		log.Error("could not drop the old log tables", "error", err)
	} else if n > 0 {
		log.Info("dropped log tables from before logs were split by kind", "tables", n)
	}

	restoreCtx, cancelRestore := context.WithTimeout(context.Background(), 30*time.Second)
	if err := brain.Restore(restoreCtx, db); err != nil {
		log.Error("could not restore the hot state", "error", err)
	}
	cancelRestore()

	// Alerts: the rules that raise them, the silences that hush them and the
	// heartbeats that notice something has gone quiet.
	alerts := alert.NewStore(alert.Options{})
	restoreAlerts(db, alerts, log)
	silences, err := silence.Open(db.DB(), time.Now)
	if err != nil {
		return err
	}
	defer func() { _ = silences.Close() }()
	system := sysstats.New(10 * time.Second)
	book, err := health.Open(db.DB(), time.Now)
	if err != nil {
		return err
	}
	statuses, err := status.Open(db.DB(), time.Now)
	if err != nil {
		return err
	}
	defer func() { _ = statuses.Close() }()
	reports, err := info.Open(context.Background(), db.DB())
	if err != nil {
		return err
	}
	gauges := gauge.NewStore(time.Now, 0)
	gauges.SetTTLFor(func(platform, sig string) time.Duration {
		if def, ok := registry.Lookup(platform, sig); ok {
			return time.Duration(def.TTLSeconds) * time.Second
		}
		return 0
	})

	// How the panels are arranged is configuration too, shared by everyone.
	dashboards := dashboard.NewStore()
	loaded, err := dashboard.LoadAll(cfg.Data.PlatformsDir)
	if err != nil {
		log.Error("dashboards not loaded", "error", err)
	}
	for platform, list := range loaded {
		dashboards.Replace(platform, list)
		log.Info("dashboards loaded", "platform", platform, "count", len(list))
	}
	brain.SetAlerting(core.Alerting{
		Alerts: alerts, Silences: silences, Statuses: statuses, Gauges: gauges, Info: reports,
	})

	sessions, err := auth.Open(auth.Options{DB: db.DB(), Password: cfg.Auth.Password})
	if err != nil {
		return err
	}
	if !sessions.Enabled() {
		if !cfg.Auth.Open {
			return errors.New("no dashboard password is set: set HM_PASS or auth.password, " +
				"or HM_OPEN=1 (auth.open) to run without one")
		}
		log.Warn("no dashboard password is set and HM_OPEN is on: every request is allowed. " +
			"Do not let anyone else reach this server")
	}
	for _, platform := range registry.Platforms() {
		set, err := rules.Load(filepath.Join(cfg.Data.PlatformsDir, platform, "rules.yaml"))
		if err != nil {
			log.Error("alert rules not loaded; using the defaults", "platform", platform, "error", err)
			set = rules.Default(platform)
		}
		brain.SetRules(set)
		log.Info("alert rules loaded", "platform", set.Platform,
			"default_ms", set.Latency.DefaultMS, "overrides", len(set.Latency.Overrides))
	}

	// Viewers are fed on the hub's own cadence, so a busy minute is one
	// update rather than one message per packet.
	viewers := hub.New(hot, registry, time.Now, cfg.Hub.BroadcastDelay)
	brain.Subscribe(viewers.Dirty)
	brain.OnAlert(viewers.Alert)
	// Alerts outlive the memory they live in: every burst is written as it
	// changes, so yesterday's can still be looked at.
	brain.OnAlert(func(a alert.Alert, _ bool) {
		alertWriter.Write(store.AlertRow{
			ID: a.ID, Platform: a.Platform, Signal: a.Signal,
			Level: string(a.Level), Category: a.Category, Message: a.Message,
			GroupKey: a.GroupKey, Source: a.Source, Target: a.Target,
			Count: a.Count, First: a.First, Last: a.Last,
			Silenced: a.Silenced, SilenceReason: a.SilenceReason, Data: a.Data,
		})
	})
	for _, platform := range registry.Platforms() {
		if n := brain.ReapplySilences(platform); n > 0 {
			log.Info("silences applied to restored alerts", "platform", platform, "alerts", n)
		}
	}

	started := time.Now()

	// Legacy (v0) packets never reach the core: the adapter turns
	// them into v1 envelopes first.
	mapping, err := legacy.LoadMapping(filepath.Join(cfg.Data.PlatformsDir, cfg.Intake.DefaultPlatform, legacy.MappingFile))
	if err != nil {
		return err
	}
	v0 := legacy.NewWithOptions(legacy.Options{
		Platform:  cfg.Intake.DefaultPlatform,
		DailyLogs: cfg.Intake.DailyLogs,
		Mapping:   mapping,
		InfoSignal: func(types ...string) string {
			return registry.InfoFor(cfg.Intake.DefaultPlatform, types...)
		},
		MetricSignal: func(types ...string) string {
			return registry.MetricFor(cfg.Intake.DefaultPlatform, types...)
		},
		OnUnknownType: func(packetType string, raw []byte) {
			brain.NoteUnknownType(cfg.Intake.DefaultPlatform, packetType, raw)
		},
	})
	tap := &intake.Tap{}
	pipeline := intake.NewPipeline(intake.PipelineOptions{
		Resolver: intake.Resolver{Default: cfg.Intake.DefaultPlatform},
		Adapters: map[string]intake.Adapter{
			cfg.Intake.DefaultPlatform: v0,
		},
		Sink:          brain,
		DropOlderThan: cfg.Intake.DropOlderThan,
		DropNewerThan: cfg.Intake.DropNewerThan,
		Tap:           tap,
	})

	udpSrv := intake.New(intake.Options{
		Addr:     cfg.Server.UDPAddr,
		Config:   cfg.Intake,
		Log:      log,
		Metrics:  metrics,
		Registry: registry,
		Pipeline: pipeline,
		Sink:     brain,
	})
	system.CountPackets(udpSrv.Datagrams)
	system.CountLogs(func() int64 { return logWriter.Stats().Written })

	history, err := visits.Open(context.Background(), db.DB(), time.Now)
	if err != nil {
		return err
	}

	httpSrv := &http.Server{
		Addr: cfg.Server.HTTPAddr,
		Handler: api.Handler(api.Deps{
			Log: log, Metrics: reg, Assets: assets, HasAssets: hasAssets, Started: started,
			Registry: registry, Store: hot, Core: brain, Hub: viewers, Intake: udpSrv,
			DB: db, Writer: writer,
			Alerts: alerts, Silences: silences, Statuses: statuses, Gauges: gauges,
			Dashboards:   dashboards,
			Visits:       history,
			Info:         reports,
			Tap:          tap,
			Health:       book,
			System:       system,
			Auth:         sessions,
			PlatformsDir: cfg.Data.PlatformsDir,
			DataDir:      cfg.Data.Backups(),
			LogWriter:    logWriter,
			AlertWriter:  alertWriter,
			WSOrigins:    cfg.Server.AllowedOrigins,
		}),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	adminSrv := &http.Server{
		Addr:              cfg.Server.AdminAddr,
		Handler:           api.AdminHandler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Info("starting healthMonitorNJ",
		"version", buildinfo.Version,
		"http", cfg.Server.HTTPAddr,
		"udp", cfg.Server.UDPAddr,
		"admin", cfg.Server.AdminAddr)

	// Any of the three failing brings the whole process down, so the first
	// error cancels the context the others are waiting on.
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	errs := make(chan error, 4)
	var wg sync.WaitGroup
	start := func(fn func() error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := fn(); err != nil {
				errs <- err
				cancel()
			}
		}()
	}
	// The writers are stopped by the defers above, so whatever feeds them has
	// to have finished first.
	var bg sync.WaitGroup
	background := func(fn func()) {
		bg.Add(1)
		go func() {
			defer bg.Done()
			fn()
		}()
	}
	background(func() { brain.Maintain(runCtx, 30*time.Second) })
	// A changed catalogue takes effect without a restart.
	background(func() {
		if err := hmsignal.Watch(runCtx, cfg.Data.PlatformsDir, registry, log); err != nil {
			log.Warn("catalogue watching stopped", "error", err)
		}
	})
	background(func() { brain.PurgeEvery(runCtx, db, time.Hour) })
	background(func() { system.Run(runCtx) })
	background(func() { brain.CheckOfflineEvery(runCtx, 10*time.Second) })
	background(func() { brain.CheckDiskEvery(runCtx, time.Minute) })
	background(func() { history.SeenEvery(runCtx, time.Minute) })
	if cfg.Data.ViewerHistoryKeepDays > 0 {
		background(func() {
			history.PurgeEvery(runCtx, time.Duration(cfg.Data.ViewerHistoryKeepDays)*24*time.Hour, time.Hour,
				func(n int64, err error) {
					if err != nil {
						log.Error("could not purge the viewing history", "error", err)
					} else if n > 0 {
						log.Info("purged the viewing history", "visits", n, "keep_days", cfg.Data.ViewerHistoryKeepDays)
					}
				})
		})
	}
	background(func() {
		reports.PurgeEvery(runCtx, func(platform, name string) int {
			if def, ok := registry.Lookup(platform, name); ok && def.Kind == hmsignal.KindInfo {
				return def.Retention.DurableDays
			}
			return hmsignal.DefaultDurableDays
		}, time.Now, time.Hour, func(n int64, err error) {
			if err != nil {
				log.Error("could not purge info reports", "error", err)
			} else if n > 0 {
				log.Info("purged info reports", "reports", n)
			}
		})
	})
	background(func() { brain.ReviewSilencesEvery(runCtx, time.Hour, 24*time.Hour) })
	background(func() { brain.RolloverEvery(runCtx, db, time.Hour) })
	background(func() {
		if err := rules.Watch(runCtx, cfg.Data.PlatformsDir, brain.SetRules, log); err != nil {
			log.Warn("rules watching stopped", "error", err)
		}
	})
	background(func() {
		if err := legacy.WatchMapping(runCtx, cfg.Data.PlatformsDir, cfg.Intake.DefaultPlatform, v0, log); err != nil {
			log.Warn("mapping watching stopped", "error", err)
		}
	})
	background(func() {
		if err := dashboard.Watch(runCtx, cfg.Data.PlatformsDir, dashboards, log); err != nil {
			log.Warn("dashboard watching stopped", "error", err)
		}
	})
	background(func() { viewers.Run(runCtx.Done()) })

	start(func() error { return serve(httpSrv, "http") })
	start(func() error { return serve(adminSrv, "admin") })
	start(func() error { return udpSrv.Run(runCtx) })

	<-runCtx.Done()
	shutCtx, shutCancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer shutCancel()
	_ = adminSrv.Shutdown(shutCtx)
	if err := httpSrv.Shutdown(shutCtx); err != nil {
		errs <- err
	}
	wg.Wait()
	cancel()
	waitFor(shutCtx, &bg, log)
	close(errs)
	if err := <-errs; err != nil {
		return err
	}
	log.Info("stopped cleanly")
	return nil
}

func waitFor(ctx context.Context, wg *sync.WaitGroup, log *slog.Logger) {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		log.Warn("background work still running at shutdown; stopping the writers anyway")
	}
}

// restoreAlerts refills the live list from the history, so a restart does not
// empty the Alerts panel.
func restoreAlerts(db *store.Store, alerts *alert.Store, log *slog.Logger) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rows, err := db.SearchAlerts(ctx, store.AlertQuery{
		From: time.Now().Add(-alert.DefaultKeep), Silenced: true, Limit: 10 * alert.DefaultMax,
	})
	if err != nil {
		log.Error("alerts not restored", "error", err)
		return
	}
	list := make([]alert.Alert, 0, len(rows))
	for _, r := range rows {
		list = append(list, alert.Alert{
			ID: r.ID, Platform: r.Platform, Signal: r.Signal,
			Level: protocol.Level(r.Level), Category: r.Category, Message: r.Message,
			GroupKey: r.GroupKey, Source: r.Source, Target: r.Target,
			Count: r.Count, First: r.First, Last: r.Last,
			Silenced: r.Silenced, SilenceReason: r.SilenceReason, Data: r.Data,
		})
	}
	log.Info("alerts restored", "alerts", alerts.Restore(list))
}

func serve(s *http.Server, name string) error {
	if s.Addr == "" {
		return nil
	}
	if err := s.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("%s server: %w", name, err)
	}
	return nil
}

func checkHealth(httpAddr string) error {
	_, port, err := net.SplitHostPort(httpAddr)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 3 * time.Second}
	res, err := client.Get("http://" + net.JoinHostPort("127.0.0.1", port) + "/healthz")
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz answered %d", res.StatusCode)
	}
	return nil
}
