package intake_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/adapter/legacy"
	"github.com/Sznapsollo/health-monitor-nj/internal/config"
	"github.com/Sznapsollo/health-monitor-nj/internal/core"
	"github.com/Sznapsollo/health-monitor-nj/internal/intake"
	"github.com/Sznapsollo/health-monitor-nj/internal/obs"
	"github.com/Sznapsollo/health-monitor-nj/internal/signal"
	"github.com/Sznapsollo/health-monitor-nj/internal/state"
	"github.com/prometheus/client_golang/prometheus"
)

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

type harness struct {
	server *intake.Server
	core   *core.Core
	store  *state.Store
	conn   net.Conn
}

// newHarness starts a real listener with the legacy adapter behind it.
func newHarness(t *testing.T, tune ...func(*intake.PipelineOptions)) *harness {
	t.Helper()
	log := slog.New(slog.NewTextHandler(discard{}, nil))

	reg := signal.NewRegistry()
	for _, d := range []*signal.Definition{
		{
			Platform: "example", Name: "requests", Kind: signal.KindTimeseries,
			Dims:   []string{"account", "url", "user", "ipAddress", "serverName", "port", "accountName"},
			Values: signal.Values{Count: "count", MS: "ms"},
		},
		{
			Platform: "example", Name: "jobs", Kind: signal.KindTimeseries,
			Dims:   []string{"jobName", "account", "accountName"},
			Values: signal.Values{Count: "count", MS: "ms"},
		},
	} {
		if err := reg.Add(d); err != nil {
			t.Fatal(err)
		}
	}

	store := state.NewStore(reg, time.Now)
	brain := core.New(store, reg, log, time.Now)

	options := intake.PipelineOptions{
		Resolver: intake.Resolver{Default: "example"},
		Adapters: map[string]intake.Adapter{"example": legacy.New("example")},
		Sink:     brain,
	}
	for _, f := range tune {
		f(&options)
	}
	pipeline := intake.NewPipeline(options)

	cfg := config.Default().Intake
	cfg.Readers = 2
	srv := intake.New(intake.Options{
		Addr:       "127.0.0.1:0",
		Config:     cfg,
		Log:        log,
		Metrics:    obs.NewMetrics(prometheus.NewRegistry()),
		Registry:   reg,
		Pipeline:   pipeline,
		Sink:       brain,
		FlushEvery: 20 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("run: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Error("the intake did not stop")
		}
	})

	select {
	case <-srv.Ready():
	case <-time.After(3 * time.Second):
		t.Fatal("the intake did not bind")
	}

	conn, err := net.Dial("udp", srv.Addr())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	return &harness{server: srv, core: brain, store: store, conn: conn}
}

func (h *harness) send(t *testing.T, body string) {
	t.Helper()
	if _, err := h.conn.Write([]byte(body)); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// waitFor polls until want is true or the test gives up, which is how a UDP
// test stays honest without sleeping for a fixed time.
func (h *harness) waitFor(t *testing.T, what string, want func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if want() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func (h *harness) count(t *testing.T, sig string) int64 {
	t.Helper()
	v, ok := h.store.View("example", sig, state.Criteria{HistoryMinutes: 10})
	if !ok {
		return 0
	}
	var n int64
	for _, p := range v.Total {
		n += p.Count
	}
	return n
}

func TestV1MetricReachesTheCube(t *testing.T) {
	h := newHarness(t)
	h.send(t, `{"v":1,"t":"metric","signal":"requests","dims":{"url":"/api/x"},"values":{"count":1,"ms":42}}`)
	h.waitFor(t, "the metric to be counted", func() bool { return h.count(t, "requests") == 1 })

	v, _ := h.store.View("example", "requests", state.Criteria{HistoryMinutes: 10, Group: "url"})
	if len(v.Groups) != 1 || v.Groups[0].Value != "/api/x" {
		t.Fatalf("groups = %+v", v.Groups)
	}
	if v.Groups[0].AvgMS != 42 {
		t.Errorf("avg = %v, want the packet's duration", v.Groups[0].AvgMS)
	}
}

func TestAPacketToAQuietReaderIsTimedWhenItArrives(t *testing.T) {
	h := newHarness(t, func(o *intake.PipelineOptions) { o.DropNewerThan = 100 * time.Millisecond })
	time.Sleep(300 * time.Millisecond)
	h.send(t, `{"v":1,"t":"metric","signal":"requests","ts":"`+time.Now().UTC().Format(time.RFC3339Nano)+`","values":{"count":1}}`)
	h.waitFor(t, "the metric to be counted", func() bool { return h.count(t, "requests") == 1 })
}

func TestLegacyPacketIsAdaptedOnTheWay(t *testing.T) {
	h := newHarness(t)
	// A v0 request packet: no version field, Java's offset format.
	h.send(t, `{"type":"request","start":"`+time.Now().Format("2006-01-02T15:04:05.000-0700")+`",
	  "executionTime":142,"level":"DEBUG","serverName":"prod-1","port":"8080","user":"unknown",
	  "account":"unknown","accountName":"unknown","ipAddress":"203.0.113.7","url":"/api/work/groups"}`)

	h.waitFor(t, "the legacy request to be charted", func() bool { return h.count(t, "requests") == 1 })
	v, _ := h.store.View("example", "requests", state.Criteria{HistoryMinutes: 10, Group: "url"})
	if len(v.Groups) != 1 || v.Groups[0].Value != "/api/work/groups" {
		t.Fatalf("groups = %+v, want the legacy url", v.Groups)
	}
}

func TestLegacyJobReportCountsItems(t *testing.T) {
	h := newHarness(t)
	h.send(t, `{"type":"jobReport","jobName":"example.queue - SEND_MAIL","start":"`+
		time.Now().Format("2006-01-02T15:04:05.000-0700")+`","itemsCount":7,"executionTime":1}`)
	h.waitFor(t, "the job report", func() bool { return h.count(t, "jobs") == 7 })
}

func TestBatchOfMetricsInOneDatagram(t *testing.T) {
	h := newHarness(t)
	h.send(t, `[{"v":1,"t":"metric","signal":"requests","dims":{"url":"/a"},"values":{"count":1,"ms":10}},
	            {"v":1,"t":"metric","signal":"requests","dims":{"url":"/b"},"values":{"count":2,"ms":20}}]`)
	h.waitFor(t, "both metrics of the batch", func() bool { return h.count(t, "requests") == 3 })
}

func TestUnknownSignalIsQuarantinedNotDropped(t *testing.T) {
	h := newHarness(t)
	h.send(t, `{"v":1,"t":"metric","signal":"mystery","values":{"count":1}}`)
	h.waitFor(t, "the quarantine entry", func() bool {
		return len(h.core.QuarantineSnapshot()) > 0
	})
	groups := h.core.QuarantineSnapshot()
	if g := groups[0]; g.Signal != "mystery" || g.Type != "metric" || g.Count != 1 ||
		g.Key != "unknown_signal/example/mystery/metric" {
		t.Errorf("group = %+v", g)
	}
}

func TestGarbageIsCounted(t *testing.T) {
	h := newHarness(t)
	h.send(t, `not json at all`)
	h.waitFor(t, "the decode error to be counted", func() bool {
		return h.server.Counters().DecodeErrs > 0
	})
}

func TestAlertsAreKeptForLaterPhases(t *testing.T) {
	h := newHarness(t)
	h.send(t, `{"v":1,"t":"alert","level":"ERROR","message":"boom"}`)
	h.waitFor(t, "the alert to be counted", func() bool {
		return h.core.PendingCounts()["alert"] > 0
	})
}

func TestChunkedPayloadIsReassembled(t *testing.T) {
	h := newHarness(t)
	whole := `{"v":1,"t":"metric","signal":"requests","dims":{"url":"/chunked"},"values":{"count":5,"ms":10}}`
	half := len(whole) / 2
	h.send(t, `{"v":1,"t":"chunk","id":"c1","part":1,"of":2,"data":`+quote(whole[:half])+`}`)
	h.send(t, `{"v":1,"t":"chunk","id":"c1","part":2,"of":2,"data":`+quote(whole[half:])+`}`)
	h.waitFor(t, "the reassembled metric", func() bool { return h.count(t, "requests") == 5 })
	if h.server.Counters().Reassembled != 1 {
		t.Errorf("reassembled = %d", h.server.Counters().Reassembled)
	}
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestADatagramLargerThanTheBufferIsCountedAsTruncated(t *testing.T) {
	h := newHarness(t)
	h.send(t, `{"v":1,"t":"log","signal":"sendLogs","data":{"body":"`+strings.Repeat("x", 20<<10)+`"}}`)

	h.waitFor(t, "a truncated datagram", func() bool { return h.server.Counters().Truncated == 1 })
	c := h.server.Counters()
	if c.DecodeErrs != 0 || c.Datagrams != 1 || h.server.Datagrams() != 1 || c.LargestDatagram != 16<<10 {
		t.Errorf("counters = %+v, want one datagram, truncated at 16 KB, and no decode error", c)
	}
}

func TestEveryPacketIsCountedBySenderAndType(t *testing.T) {
	h := newHarness(t)
	now := time.Now().Format("2006-01-02T15:04:05.000-0700")
	h.send(t, `{"type":"request","start":"`+now+`","executionTime":5,"serverName":"app-1","url":"/a"}`)
	h.send(t, `{"type":"request","start":"`+now+`","executionTime":5,"serverName":"app-1","url":"/b"}`)
	h.send(t, `{"type":"configServerListStatus","start":"`+now+`","serverName":"app-1"}`)
	h.send(t, `{"v":1,"t":"status","signal":"servers","key":"app-2","source":"app-2"}`)
	h.send(t, `{"v":1,"t":"metric","platform":"nowhere","signal":"requests","values":{"count":1}}`)

	h.waitFor(t, "four packets counted", func() bool { return h.count(t, "packets") == 4 })
	bySender := map[string]int64{}
	v, _ := h.store.View("example", "packets", state.Criteria{HistoryMinutes: 10, Group: "sender"})
	for _, g := range v.Groups {
		bySender[g.Value] = g.Count
	}
	if bySender["app-1"] != 3 || bySender["app-2"] != 1 {
		t.Errorf("by sender = %v, want 3 from app-1 (each request once, not with its log row) and 1 from app-2", bySender)
	}
	byType := map[string]int64{}
	v, _ = h.store.View("example", "packets", state.Criteria{HistoryMinutes: 10, Group: "type"})
	for _, g := range v.Groups {
		byType[g.Value] = g.Count
	}
	if byType["request"] != 2 || byType["configServerListStatus"] != 1 || byType["status:servers"] != 1 {
		t.Errorf("by type = %v", byType)
	}
	if _, ok := h.store.View("nowhere", "packets", state.Criteria{HistoryMinutes: 10}); ok {
		t.Error("a packet for a made-up platform created it")
	}
}

func TestTheTapShowsDatagramsOnlyWhileWatched(t *testing.T) {
	tap := &intake.Tap{}
	h := newHarness(t, func(o *intake.PipelineOptions) { o.Tap = tap })
	h.send(t, `{"v":1,"t":"alert","level":"ERROR","message":"unseen"}`)
	h.waitFor(t, "the unwatched alert", func() bool { return h.core.PendingCounts()["alert"] > 0 })
	if got, _ := tap.Since(0); len(got) != 0 {
		t.Fatalf("entries = %+v, want none while nobody watches", got)
	}

	tap.Watch(time.Now())
	h.send(t, `{"type":"customWarning","level":"INFO","message":"no sender"}`)
	h.send(t, `not json at all`)
	var got []intake.TapEntry
	h.waitFor(t, "both datagrams on the tap", func() bool {
		got, _ = tap.Since(0)
		return len(got) == 2
	})
	byRejected := map[string]intake.TapEntry{}
	for _, e := range got {
		byRejected[e.Rejected] = e
	}
	warning, garbage := byRejected[""], byRejected["decode_error"]
	if warning.Type != "customWarning" || warning.Sender != "" || warning.From != "127.0.0.1" ||
		!strings.Contains(warning.Raw, "no sender") || warning.Platform != "example" {
		t.Errorf("warning = %+v", warning)
	}
	if garbage.Raw != "not json at all" {
		t.Errorf("garbage = %+v", garbage)
	}
	if later, seq := tap.Since(got[1].Seq); len(later) != 0 || seq != got[1].Seq {
		t.Errorf("since the last = %+v (%d), want nothing new", later, seq)
	}
}
