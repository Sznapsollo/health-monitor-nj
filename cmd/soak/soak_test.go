package main

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/adapter/legacy"
	"github.com/Sznapsollo/health-monitor-nj/internal/protocol"
)

func TestShippedConfigLoads(t *testing.T) {
	cfg, err := loadConfig(filepath.Join("..", "..", "soak.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]bool{}
	for _, s := range buildStreams(newTraffic(cfg, false, time.Now())) {
		kinds[s.kind] = true
	}
	for _, k := range []string{"requests", "alerts", "sendLogs", "queues", "heartbeats"} {
		if !kinds[k] {
			t.Errorf("soak.yaml sends no %s", k)
		}
	}
}

const minimal = "platform: p\nprotocol: v1\nservers: { count: 1 }\n"

func TestConfigErrors(t *testing.T) {
	cases := map[string]string{
		"missing":  "",
		"protocol": "platform: p\nservers: { count: 1 }",
		"platform": "protocol: v1\nservers: { count: 1 }",
		"servers":  "platform: p\nprotocol: v1\nservers: { count: 0 }",
		"unknown":  minimal + "requsts: { urls: 3 }",
		"level":    minimal + "alerts: { perMinute: 1, messages: [x], levels: { LOUD: 1 } }",
		"noLevels": minimal + "alerts: { perMinute: 1, messages: [x] }",
		"messages": minimal + "alerts: { perMinute: 1, levels: { ERROR: 1 } }",
		"users":    minimal + "requests: { perServerPerMinute: 1, urls: 1, accounts: 1, urlSkew: 2, accountSkew: 2 }",
		"skew":     minimal + "requests: { perServerPerMinute: 1, urls: 1, accounts: 1, usersPerAccount: 1, urlSkew: 2, accountSkew: 0.5 }",
		"noSkew":   minimal + "requests: { perServerPerMinute: 1, urls: 1, accounts: 1, usersPerAccount: 1 }",
		"mails":    minimal + "sendLogs: { perMinute: 1 }",
		"sources":  minimal + "queues: { count: 1, every: 1m }",
		"flap":     minimal + "heartbeats: { every: 30s, flap: true }",
		"range":    minimal + "requests: { fastMs: [10] }",
		"reversed": minimal + "requests: { slowMs: [10, 1] }",
		"type":     minimal + "signals: [{ name: x, type: counter, rate: 1 }]",
		"rate":     minimal + "signals: [{ name: x, type: metric }]",
		"dup":      minimal + "signals: [{ name: x, type: metric, rate: 1 }, { name: x, type: log, rate: 1 }]",
		"dim":      minimal + "signals: [{ name: x, type: metric, rate: 1, dims: { a: -2 } }]",
		"names":    minimal + "signals: [{ name: x, type: metric, rate: 1, dims: { an: { names: a } } }]",
		"namesOf":  minimal + "signals: [{ name: x, type: metric, rate: 1, dims: { a: 2, b: { names: a }, c: { names: b } } }]",
		"namesKey": minimal + "signals: [{ name: x, type: metric, rate: 1, dims: { a: 2, b: { of: a } } }]",
		"gauge":    minimal + "signals: [{ name: x, type: gauge, rate: 1 }]",
		"alertSig": minimal + "signals: [{ name: x, type: alert, rate: 1, dims: { a: 1 } }]",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := loadConfig(writeConfig(t, body)); err == nil {
				t.Error("accepted")
			}
		})
	}
	if _, err := loadConfig(""); err == nil {
		t.Error("no -config accepted")
	}
}

func TestAbsentSectionsSendNothing(t *testing.T) {
	cfg, err := loadConfig(writeConfig(t, minimal))
	if err != nil {
		t.Fatal(err)
	}
	if streams := buildStreams(newTraffic(cfg, true, time.Now())); len(streams) != 0 {
		t.Errorf("%d streams from a file that asks for none", len(streams))
	}
}

func TestV0ThroughAdapter(t *testing.T) {
	tr := testTraffic(t, false)
	rng := rand.New(rand.NewSource(1))
	now := time.Now()
	adapter := legacy.New("")

	cases := []struct {
		name   string
		raw    []byte
		typ    protocol.Type
		signal string
	}{
		{"request", tr.request(rng, tr.servers[2], now), protocol.TypeMetric, "requests"},
		{"alert", tr.alert(rng, now), protocol.TypeAlert, "alerts"},
		{"sendLog", tr.sendLog(rng, now), protocol.TypeLog, "sendLogs"},
		{"heartbeat", tr.heartbeat(tr.servers[0], now), protocol.TypeStatus, "servers"},
		{"queues", tr.queueReport(rng, tr.queueSources()[0], now), protocol.TypeGauge, "jobQueuesLoad"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := protocol.Decode(c.raw); !errors.Is(err, protocol.ErrLegacy) {
				t.Fatalf("not a v0 packet: %v", err)
			}
			packets, err := adapter.Adapt(c.raw)
			if err != nil || len(packets) != 1 {
				t.Fatalf("adapt: %v, %d packets", err, len(packets))
			}
			checkPacket(t, packets[0], c.typ, c.signal)
		})
	}
}

func TestV1Decodes(t *testing.T) {
	tr := testTraffic(t, true)
	rng := rand.New(rand.NewSource(1))
	now := time.Now()

	cases := []struct {
		name   string
		raw    []byte
		typ    protocol.Type
		signal string
	}{
		{"request", tr.request(rng, tr.servers[2], now), protocol.TypeMetric, "requests"},
		{"alert", tr.alert(rng, now), protocol.TypeAlert, "alerts"},
		{"sendLog", tr.sendLog(rng, now), protocol.TypeLog, "sendLogs"},
		{"heartbeat", tr.heartbeat(tr.servers[0], now), protocol.TypeStatus, "servers"},
		{"queues", tr.queueReport(rng, tr.queueSources()[0], now), protocol.TypeGauge, "jobQueuesLoad"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, err := protocol.Decode(c.raw)
			if err != nil {
				t.Fatal(err)
			}
			if p.Platform != "example" {
				t.Errorf("platform = %q", p.Platform)
			}
			checkPacket(t, p, c.typ, c.signal)
		})
	}
}

func checkPacket(t *testing.T, p protocol.Packet, typ protocol.Type, signal string) {
	t.Helper()
	if p.T != typ || p.Signal() != signal {
		t.Fatalf("got %s %q, want %s %q", p.T, p.Signal(), typ, signal)
	}
	if p.TS.IsZero() {
		t.Error("no timestamp")
	}
	switch typ {
	case protocol.TypeMetric:
		d := p.Metric.Dims
		if d["port"] != "8082" || d["serverName"] != "soak-3" || d["url"] == "" || d["account"] == "" || d["user"] == "" {
			t.Errorf("dims = %v", d)
		}
		if p.Metric.Values["ms"] <= 0 || p.Metric.Values["count"] != 1 {
			t.Errorf("values = %v", p.Metric.Values)
		}
	case protocol.TypeAlert:
		if p.Alert.Message == "" || p.Alert.Category == "" {
			t.Errorf("alert = %+v", p.Alert)
		}
	case protocol.TypeLog:
		for _, key := range []string{"to", "subject", "account", "user"} {
			if p.Log.Data[key] == nil {
				t.Errorf("log data has no %s: %v", key, p.Log.Data)
			}
		}
	case protocol.TypeStatus:
		if p.Status.Key != "soak-1" {
			t.Errorf("status key = %q", p.Status.Key)
		}
	case protocol.TypeGauge:
		if len(p.Gauge.Points) != 200 || p.Source == "" {
			t.Errorf("gauge: %d points from %q", len(p.Gauge.Points), p.Source)
		}
	}
}

func TestCustomSignals(t *testing.T) {
	path := writeConfig(t, `
platform: demo
protocol: v0
servers: { count: 2 }
alerts: { levels: { ERROR: 1 } }
signals:
  - { name: checkout, type: metric, rate: 60, dims: { url: 4, region: [eu, us] }, values: { ms: [5, 800], count: [1, 3] } }
  - { name: disk, type: gauge, rate: 2, platform: other, dims: { volume: [data, logs] }, values: { gb: [0, 500] } }
  - { name: node, type: status, rate: 2, dims: { host: 3 } }
  - { name: audit, type: log, rate: 5, dims: { who: 10 } }
  - { name: fraud, type: alert, rate: 5, dims: { rule: [a, b] } }
`)
	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	tr := newTraffic(cfg, false, time.Now())
	rng := rand.New(rand.NewSource(1))

	statuses := 0
	for i := range cfg.Signals {
		st := newCustomState(&cfg.Signals[i])
		for range 50 {
			p, err := protocol.Decode(tr.custom(rng, st, time.Now()))
			if err != nil {
				t.Fatalf("%s: %v", st.sig.Name, err)
			}
			if string(p.T) != st.sig.Type || p.Signal() != st.sig.Name {
				t.Fatalf("%s: got %s %q", st.sig.Name, p.T, p.Signal())
			}
			wantPlatform := "demo"
			if st.sig.Name == "disk" {
				wantPlatform = "other"
			}
			if p.Platform != wantPlatform {
				t.Errorf("%s: platform %q", st.sig.Name, p.Platform)
			}
			switch st.sig.Type {
			case "metric":
				c := p.Metric.Values["count"]
				if c < 1 || c > 3 || c != float64(int(c)) {
					t.Errorf("count %v", c)
				}
				if ms := p.Metric.Values["ms"]; ms < 5 || ms > 800 {
					t.Errorf("ms %v", ms)
				}
				if r := p.Metric.Dims["region"]; r != "eu" && r != "us" {
					t.Errorf("region %q", r)
				}
				if !strings.HasPrefix(p.Metric.Dims["url"], "url-") {
					t.Errorf("url %q", p.Metric.Dims["url"])
				}
			case "gauge":
				if p.Source != tr.servers[0].name {
					t.Errorf("gauge source %q, want always %q", p.Source, tr.servers[0].name)
				}
				if len(p.Gauge.Points) != 2 {
					t.Errorf("points %v", p.Gauge.Points)
				}
				for _, pt := range p.Gauge.Points {
					if pt.Value < 0 || pt.Value > 500 {
						t.Errorf("gauge value %v", pt.Value)
					}
				}
			case "status":
				if want := fmt.Sprintf("host-%d", statuses%3+1); p.Status.Key != want {
					t.Errorf("status key %q, want %q: every key in turn", p.Status.Key, want)
				}
				statuses++
			}
		}
	}
}

func TestQueuesDrift(t *testing.T) {
	tr := testTraffic(t, true)
	rng := rand.New(rand.NewSource(1))
	src := tr.queueSources()[0]
	var prev []protocol.GaugePoint
	bigJumps := 0
	for range 100 {
		p, err := protocol.Decode(tr.queueReport(rng, src, time.Now()))
		if err != nil {
			t.Fatal(err)
		}
		for i, pt := range p.Gauge.Points {
			if pt.Value < 0 || pt.Value > 5000 {
				t.Fatalf("value %v out of range", pt.Value)
			}
			if pt.Warn != (pt.Value > 1000) {
				t.Fatalf("warn %v for %v", pt.Warn, pt.Value)
			}
			if prev != nil && abs(pt.Value-prev[i].Value) > 2500 {
				bigJumps++
			}
		}
		prev = p.Gauge.Points
	}
	if bigJumps > 0 {
		t.Errorf("%d reports jumped by more than half the range", bigJumps)
	}
}

func TestLatencyShare(t *testing.T) {
	tr := testTraffic(t, true)
	tr.cfg.Requests.SlowShare = 0.1
	rng := rand.New(rand.NewSource(1))
	slow := 0
	for range 10000 {
		ms := tr.latency(rng)
		if ms >= 1200 {
			slow++
		} else if ms > 400 || ms < 5 {
			t.Fatalf("fast latency %v", ms)
		}
	}
	if slow < 800 || slow > 1200 {
		t.Errorf("slow = %d of 10000, want about 1000", slow)
	}
}

func TestFlap(t *testing.T) {
	tr := testTraffic(t, true)
	tr.cfg.Heartbeats.Flap = true
	tr.cfg.Heartbeats.FlapEvery = 10 * time.Minute
	tr.cfg.Heartbeats.FlapDown = 3 * time.Minute
	emit := tr.heartbeats()
	rng := rand.New(rand.NewSource(1))

	start := time.Now()
	sentAt := func(at time.Duration) int {
		n := 0
		emit(rng, start.Add(at), func([]byte) { n++ })
		return n
	}
	want := map[time.Duration]int{0: 5, 9 * time.Minute: 5, 10 * time.Minute: 4, 12 * time.Minute: 4, 13 * time.Minute: 5, 22 * time.Minute: 5, 23 * time.Minute: 4}
	for _, at := range []time.Duration{0, 9 * time.Minute, 10 * time.Minute, 12 * time.Minute, 13 * time.Minute, 22 * time.Minute, 23 * time.Minute} {
		if got := sentAt(at); got != want[at] {
			t.Errorf("at %s: %d heartbeats, want %d", at, got, want[at])
		}
	}
}

func TestRatedStreamPaces(t *testing.T) {
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var c kindStats
	s := stream{kind: "x", perMinute: 6000, emit: func(_ *rand.Rand, _ time.Time, send func([]byte)) { send([]byte("{}")) }}
	if err := s.run(ctx, conn.LocalAddr().String(), &c, 0); err != nil {
		t.Fatal(err)
	}
	if n := c.sent.Load(); n < 70 || n > 110 {
		t.Errorf("sent %d in a second at 100/s", n)
	}
}

func testTraffic(t *testing.T, v1 bool) *traffic {
	t.Helper()
	cfg, err := loadConfig(filepath.Join("testdata", "traffic.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return newTraffic(cfg, v1, time.Now())
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "soak.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

func TestDesignerExample(t *testing.T) {
	cfg, err := loadConfig(filepath.Join("..", "..", "soak.designer.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	tr := newTraffic(cfg, false, time.Now())
	kinds := map[string]bool{}
	for _, s := range buildStreams(tr) {
		kinds[s.kind] = true
	}
	if kinds["requests"] || kinds["alerts"] || kinds["sendLogs"] || kinds["queues"] || kinds["heartbeats"] {
		t.Errorf("the designer example is not quiet: %v", kinds)
	}

	rng := rand.New(rand.NewSource(1))
	calls := newCustomState(&cfg.Signals[0])
	p, err := protocol.Decode(tr.custom(rng, calls, time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	if p.Metric == nil || p.Metric.Signal != "apiCalls" || p.Metric.Dims["port"] == "" ||
		!strings.HasPrefix(p.Metric.Dims["account"], "account-") || p.Metric.Values["count"] != 1 {
		t.Errorf("apiCalls = %+v", p.Metric)
	}
	for range 50 {
		p, err := protocol.Decode(tr.custom(rng, calls, time.Now()))
		if err != nil {
			t.Fatal(err)
		}
		n := strings.TrimPrefix(p.Metric.Dims["account"], "account-")
		if want := "Soak Account " + n; p.Metric.Dims["accountName"] != want {
			t.Fatalf("accountName = %q for %q, want %q", p.Metric.Dims["accountName"], p.Metric.Dims["account"], want)
		}
	}

	vpn := newCustomState(&cfg.Signals[1])
	p, err = protocol.Decode(tr.custom(rng, vpn, time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	if p.Gauge == nil || p.Gauge.Signal != "vpnUsage" || len(p.Gauge.Points) != 25 ||
		p.Gauge.Points[0].Label != "user-1" {
		t.Errorf("vpnUsage = %+v", p.Gauge)
	}
}

func TestMySignalsExample(t *testing.T) {
	cfg, err := loadConfig(filepath.Join("..", "..", "soak.mysignals.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, s := range buildStreams(newTraffic(cfg, true, time.Now())) {
		kinds = append(kinds, s.kind)
	}
	want := []string{"signal:checkout", "signal:diskFree", "signal:vpnUsersLoad", "signal:loginLogs",
		"signal:shopModules", "signal:shopRelease"}
	if !reflect.DeepEqual(kinds, want) {
		t.Errorf("streams = %v, want only %v", kinds, want)
	}

	logins := newCustomState(&cfg.Signals[3])
	p, err := protocol.Decode(newTraffic(cfg, true, time.Now()).custom(rand.New(rand.NewSource(1)), logins, time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	if p.Log == nil || p.Log.Signal != "loginLogs" ||
		!strings.HasPrefix(p.Log.Data["user"].(string), "user-") || p.Log.Data["result"] == nil {
		t.Errorf("loginLogs = %+v", p.Log)
	}
}

func TestAccountSkewSetsHowFewDominate(t *testing.T) {
	busiest := func(skew float64) int {
		tr := testTraffic(t, true)
		tr.cfg.Requests.AccountSkew = skew
		rng := rand.New(rand.NewSource(1))
		counts := map[string]int{}
		for range 60000 {
			counts[tr.account(rng).id]++
		}
		top := 0
		for _, n := range counts {
			top = max(top, n)
		}
		return top
	}
	even, two, four := busiest(1), busiest(2), busiest(4)
	if even > 40 {
		t.Errorf("skew 1: busiest account has %d of 60000, want about 10", even)
	}
	if two < 50*10 || four < 5*two {
		t.Errorf("busiest account: skew 2 → %d, skew 4 → %d; want each far above the last", two, four)
	}
}

func TestInfoReportsReachAnInfoSignalWholeOrOneEntryAtATime(t *testing.T) {
	path := writeConfig(t, `
platform: demo
protocol: v0
servers: { count: 3 }
signals:
  - { name: modules, type: info, packetType: shopModulesReport, rate: 1, partial: true, dims: { module: [cart, search, payments] }, values: { queue: [0, 50] } }
`)
	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	tr := newTraffic(cfg, false, time.Now())
	rng := rand.New(rand.NewSource(1))
	st := newCustomState(&cfg.Signals[0])
	adapter := legacy.NewWithOptions(legacy.Options{InfoSignal: func(types ...string) string {
		if slices.Contains(types, "shopModulesReport") {
			return "modules"
		}
		return ""
	}})

	var full, single int
	for range 60 {
		packets, err := adapter.Adapt(tr.custom(rng, st, time.Now()))
		if err != nil || len(packets) != 1 || packets[0].Status == nil {
			t.Fatalf("got %+v (%v), want one info status", packets, err)
		}
		s := packets[0].Status
		if s.Signal != "modules" || s.Key != tr.servers[0].name {
			t.Fatalf("status %s/%s, want modules from %s", s.Signal, s.Key, tr.servers[0].name)
		}
		list, _ := s.Content["moduleStatusMap"].(map[string]any)
		switch len(list) {
		case 3:
			full++
		case 1:
			single++
			for _, e := range list {
				if _, ok := e.(map[string]any)["config"]; !ok {
					t.Errorf("a single entry has no config: %v", e)
				}
			}
		default:
			t.Fatalf("list = %v", list)
		}
	}
	if full == 0 || single == 0 {
		t.Fatalf("full %d, single %d; want both", full, single)
	}

	_, err = loadConfig(writeConfig(t, `
servers: { count: 1 }
signals:
  - { name: modules, type: info, rate: 1, dims: { module: [cart] } }
`))
	if err == nil || !strings.Contains(err.Error(), "packetType") {
		t.Fatalf("err = %v, want packetType asked for", err)
	}
}

func TestFirstDimensionIsTheOneWrittenFirst(t *testing.T) {
	cfg, err := loadConfig(writeConfig(t, `
platform: demo
protocol: v1
servers: { count: 1 }
signals:
  - { name: logins, type: log, rate: 60, dims: { user: 3, result: [ok, failed] }, values: { ms: [1, 2], count: [5, 6] } }
`))
	if err != nil {
		t.Fatal(err)
	}
	tr := newTraffic(cfg, true, time.Now())
	st := newCustomState(&cfg.Signals[0])
	if st.values[0] != "ms" {
		t.Errorf("first value %q, want ms", st.values[0])
	}
	rng := rand.New(rand.NewSource(1))
	for range 20 {
		p, err := protocol.Decode(tr.custom(rng, st, time.Now()))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(p.Log.Key, "user-") {
			t.Fatalf("log key %q, want a user", p.Log.Key)
		}
	}
}
