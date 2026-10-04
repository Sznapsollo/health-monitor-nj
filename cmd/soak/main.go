// Command soak sends legacy-shaped traffic at a monitor for hours or days,
// to see how memory, SQLite growth and retention hold up. cmd/loadgen is the
// short fixed-rate burst; this one runs until stopped.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

type options struct {
	config   string
	addr     string
	status   string
	token    string
	password string
	report   time.Duration
}

func main() {
	var o options
	flag.StringVar(&o.config, "config", "", "YAML file describing everything the run sends (see soak.yaml)")
	flag.StringVar(&o.addr, "addr", "127.0.0.1:8082", "UDP address of the monitor")
	flag.StringVar(&o.status, "status", "http://127.0.0.1:8081", "HTTP base URL for reading the monitor's counters back; empty to skip")
	flag.StringVar(&o.token, "token", os.Getenv("HM_SOAK_TOKEN"), "display or login token for -status when the monitor has a password (HM_SOAK_TOKEN)")
	flag.StringVar(&o.password, "password", os.Getenv("HM_PASS"), "the monitor's password, to log in for -status instead of a token (HM_PASS)")
	flag.DurationVar(&o.report, "report", 10*time.Second, "how often to print what was sent and the monitor's counters")
	flag.Parse()

	if err := run(o); err != nil {
		fmt.Fprintln(os.Stderr, "soak:", err)
		os.Exit(1)
	}
}

func run(o options) error {
	cfg, err := loadConfig(o.config)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if cfg.Duration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.Duration)
		defer cancel()
	}

	started := time.Now()
	t := newTraffic(cfg, cfg.Protocol == "v1", started)
	streams := buildStreams(t)
	stats := newStats(streams)

	until := "until stopped"
	if cfg.Duration > 0 {
		until = "for " + cfg.Duration.String()
	}
	fmt.Printf("soak: %s, protocol %s to %s %s; %d servers, %.0f requests/min, %d custom signals\n",
		o.config, cfg.Protocol, o.addr, until, len(t.servers), cfg.Requests.PerServerPerMinute*float64(len(t.servers)), len(cfg.Signals))

	var wg sync.WaitGroup
	for i, s := range streams {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.run(ctx, o.addr, stats.of(s.kind), int64(i)); err != nil {
				say("stream %s: %v", s.kind, err)
			}
		}()
	}

	if o.status != "" && o.token == "" && o.password != "" {
		if err := login(o.status, o.password); err != nil {
			say("login to %s failed: %v; counters will be unavailable", o.status, err)
		}
	}
	base, _ := readCounters(o.status, o.token)
	reporter := newReporter(stats, o.status, o.token, base, started)
	ticker := time.NewTicker(max(o.report, time.Second))
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			stoppedAt := time.Now()
			time.Sleep(2 * time.Second)
			say("stopped after %s", stoppedAt.Sub(started).Round(time.Second))
			reporter.print(stoppedAt, true)
			return nil
		case <-ticker.C:
			reporter.print(time.Now(), false)
		}
	}
}

// stream is one kind of traffic. A rated stream sends perMinute datagrams
// spread evenly; a periodic one calls emit every `every`, starting at once.
type stream struct {
	kind      string
	perMinute float64
	every     time.Duration
	emit      func(rng *rand.Rand, now time.Time, send func([]byte))
}

// tick paces rated streams coarsely: one timer per packet costs more than the
// packet at high rates (see cmd/loadgen).
const tick = 20 * time.Millisecond

func (s stream) run(ctx context.Context, addr string, c *kindStats, seed int64) error {
	conn, err := net.Dial("udp", addr)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	rng := rand.New(rand.NewSource(time.Now().UnixNano() + seed*7919))
	send := func(b []byte) {
		if _, err := conn.Write(b); err != nil {
			c.failed.Add(1)
			return
		}
		c.sent.Add(1)
	}

	if s.every > 0 {
		s.emit(rng, time.Now(), send)
		ticker := time.NewTicker(s.every)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case now := <-ticker.C:
				s.emit(rng, now, send)
			}
		}
	}

	perTick := s.perMinute / 60 * tick.Seconds()
	credit := rng.Float64()
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case now := <-ticker.C:
			credit += perTick
			for ; credit >= 1; credit-- {
				s.emit(rng, now, send)
			}
		}
	}
}

func buildStreams(t *traffic) []stream {
	cfg := t.cfg
	var out []stream
	if cfg.Requests.PerServerPerMinute > 0 {
		for _, srv := range t.servers {
			out = append(out, stream{kind: "requests", perMinute: cfg.Requests.PerServerPerMinute,
				emit: func(rng *rand.Rand, now time.Time, send func([]byte)) { send(t.request(rng, srv, now)) }})
		}
	}
	if cfg.Alerts.PerMinute > 0 {
		out = append(out, stream{kind: "alerts", perMinute: cfg.Alerts.PerMinute,
			emit: func(rng *rand.Rand, now time.Time, send func([]byte)) { send(t.alert(rng, now)) }})
	}
	if cfg.SendLogs.PerMinute > 0 {
		out = append(out, stream{kind: "sendLogs", perMinute: cfg.SendLogs.PerMinute,
			emit: func(rng *rand.Rand, now time.Time, send func([]byte)) { send(t.sendLog(rng, now)) }})
	}
	if cfg.Queues.Count > 0 {
		sources := t.queueSources()
		out = append(out, stream{kind: "queues", every: cfg.Queues.Every,
			emit: func(rng *rand.Rand, now time.Time, send func([]byte)) {
				for _, src := range sources {
					send(t.queueReport(rng, src, now))
				}
			}})
	}
	if cfg.Heartbeats.Every > 0 {
		out = append(out, stream{kind: "heartbeats", every: cfg.Heartbeats.Every, emit: t.heartbeats()})
	}
	for i := range cfg.Signals {
		st := newCustomState(&cfg.Signals[i])
		out = append(out, stream{kind: "signal:" + st.sig.Name, perMinute: st.sig.Rate,
			emit: func(rng *rand.Rand, now time.Time, send func([]byte)) { send(t.custom(rng, st, now)) }})
	}
	return out
}

func (t *traffic) heartbeats() func(*rand.Rand, time.Time, func([]byte)) {
	hb := t.cfg.Heartbeats
	down := -1
	var downUntil, nextFlap time.Time
	return func(rng *rand.Rand, now time.Time, send func([]byte)) {
		if hb.Flap {
			if nextFlap.IsZero() {
				nextFlap = now.Add(hb.FlapEvery)
			}
			if down >= 0 && !now.Before(downUntil) {
				say("flap: %s is back", t.servers[down].name)
				down = -1
			}
			if down < 0 && !now.Before(nextFlap) {
				down = rng.Intn(len(t.servers))
				downUntil = now.Add(hb.FlapDown)
				nextFlap = downUntil.Add(hb.FlapEvery)
				say("flap: %s stops its heartbeats for %s", t.servers[down].name, hb.FlapDown)
			}
		}
		for i, srv := range t.servers {
			if i != down {
				send(t.heartbeat(srv, now))
			}
		}
	}
}

type kindStats struct {
	sent, failed atomic.Int64
}

type stats struct {
	kinds []string
	by    map[string]*kindStats
}

func newStats(streams []stream) *stats {
	s := &stats{by: map[string]*kindStats{}}
	for _, st := range streams {
		if _, ok := s.by[st.kind]; !ok {
			s.kinds = append(s.kinds, st.kind)
			s.by[st.kind] = &kindStats{}
		}
	}
	return s
}

func (s *stats) of(kind string) *kindStats { return s.by[kind] }

type reporter struct {
	stats   *stats
	status  string
	token   string
	base    counters
	started time.Time
	last    map[string]int64
	lastAt  time.Time
}

func newReporter(s *stats, status, token string, base counters, started time.Time) *reporter {
	return &reporter{stats: s, status: status, token: token, base: base, started: started, last: map[string]int64{}, lastAt: started}
}

func (r *reporter) print(now time.Time, final bool) {
	window := now.Sub(r.lastAt).Minutes()
	total := now.Sub(r.started).Minutes()
	var parts []string
	var sent, failed int64
	for _, kind := range r.stats.kinds {
		c := r.stats.by[kind]
		n := c.sent.Load()
		sent += n
		failed += c.failed.Load()
		rate := float64(n-r.last[kind]) / window
		if final {
			rate = float64(n) / total
		}
		parts = append(parts, fmt.Sprintf("%s %d (%.0f/min)", kind, n, rate))
		r.last[kind] = n
	}
	r.lastAt = now

	line := fmt.Sprintf("[%s] sent %d", now.Sub(r.started).Round(time.Second), sent)
	if failed > 0 {
		line += fmt.Sprintf(", %d send errors", failed)
	}
	say("%s · %s", line, strings.Join(parts, " · "))

	if r.status == "" {
		return
	}
	c, ok := readCounters(r.status, r.token)
	if !ok {
		say("  monitor: counters unavailable at %s", r.status)
		return
	}
	d := c.minus(r.base)
	say("  monitor: decoded %d, legacy %d, quarantined %d, decode errors %d, too old %d, too new %d, kernel drops %d · writer dropped %d, errors %d · alert writer dropped %d",
		d.Intake.Decoded, d.Intake.Legacy, d.Intake.Quarantined, d.Intake.DecodeErrs, d.Intake.TooOld, d.Intake.TooNew,
		d.Intake.KernelDrops, d.Writer.Dropped, d.Writer.Errors+d.LogWriter.Errors, d.AlertWriter.Dropped)
	say("  monitor: memory %s, hot state %s, database %s", mb(residentBytes(r.status)), mb(c.HotBytes), mb(c.DBBytes))
}

type counters struct {
	Intake struct {
		Decoded     int64 `json:"decoded"`
		Legacy      int64 `json:"legacy"`
		DecodeErrs  int64 `json:"decodeErrors"`
		Quarantined int64 `json:"quarantined"`
		TooOld      int64 `json:"tooOld"`
		TooNew      int64 `json:"tooNew"`
		KernelDrops int64 `json:"kernelDrops"`
	} `json:"intake"`
	Writer      writerCounters `json:"writer"`
	LogWriter   writerCounters `json:"logWriter"`
	AlertWriter writerCounters `json:"alertWriter"`
	HotBytes    int64          `json:"hotBytes"`
	DBBytes     int64          `json:"dbBytes"`
}

type writerCounters struct {
	Dropped int64 `json:"dropped"`
	Errors  int64 `json:"errors"`
}

func (c counters) minus(b counters) counters {
	c.Intake.Decoded -= b.Intake.Decoded
	c.Intake.Legacy -= b.Intake.Legacy
	c.Intake.DecodeErrs -= b.Intake.DecodeErrs
	c.Intake.Quarantined -= b.Intake.Quarantined
	c.Intake.TooOld -= b.Intake.TooOld
	c.Intake.TooNew -= b.Intake.TooNew
	c.Intake.KernelDrops -= b.Intake.KernelDrops
	for _, w := range []struct{ a, b *writerCounters }{{&c.Writer, &b.Writer}, {&c.LogWriter, &b.LogWriter}, {&c.AlertWriter, &b.AlertWriter}} {
		w.a.Dropped -= w.b.Dropped
		w.a.Errors -= w.b.Errors
	}
	return c
}

var httpClient = func() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Timeout: 5 * time.Second, Jar: jar}
}()

func login(base, password string) error {
	body := strings.NewReader(`{"name":"soak","password":` + strconv.Quote(password) + `}`)
	res, err := httpClient.Post(strings.TrimRight(base, "/")+"/api/login", "application/json", body)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("login answered %d", res.StatusCode)
	}
	return nil
}

// residentBytes reads the monitor's own memory from its Prometheus metrics.
func residentBytes(base string) int64 {
	res, err := httpClient.Get(strings.TrimRight(base, "/") + "/api/metrics")
	if err != nil {
		return 0
	}
	defer func() { _ = res.Body.Close() }()
	scanner := bufio.NewScanner(res.Body)
	for scanner.Scan() {
		if v, ok := strings.CutPrefix(scanner.Text(), "process_resident_memory_bytes "); ok {
			f, _ := strconv.ParseFloat(v, 64)
			return int64(f)
		}
	}
	return 0
}

func mb(n int64) string {
	return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
}

func readCounters(base, token string) (counters, bool) {
	var c counters
	if base == "" {
		return c, false
	}
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(base, "/")+"/api/state", nil)
	if err != nil {
		return c, false
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := httpClient.Do(req)
	if err != nil {
		return c, false
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return c, false
	}
	if err := json.NewDecoder(res.Body).Decode(&c); err != nil {
		return c, false
	}
	return c, true
}

var sayMu sync.Mutex

func say(format string, args ...any) {
	sayMu.Lock()
	defer sayMu.Unlock()
	fmt.Printf("%s "+format+"\n", append([]any{time.Now().Format("15:04:05")}, args...)...)
}
