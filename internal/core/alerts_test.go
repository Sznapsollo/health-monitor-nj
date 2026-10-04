package core_test

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/alert"
	"github.com/Sznapsollo/health-monitor-nj/internal/core"
	"github.com/Sznapsollo/health-monitor-nj/internal/gauge"
	"github.com/Sznapsollo/health-monitor-nj/internal/protocol"
	"github.com/Sznapsollo/health-monitor-nj/internal/rules"
	"github.com/Sznapsollo/health-monitor-nj/internal/signal"
	"github.com/Sznapsollo/health-monitor-nj/internal/silence"
	"github.com/Sznapsollo/health-monitor-nj/internal/state"
	"github.com/Sznapsollo/health-monitor-nj/internal/status"
	"github.com/Sznapsollo/health-monitor-nj/internal/store"
)

var base = time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

type clock struct{ t time.Time }

func (c *clock) now() time.Time      { return c.t }
func (c *clock) add(d time.Duration) { c.t = c.t.Add(d) }

type fixture struct {
	core     *core.Core
	alerts   *alert.Store
	silences *silence.Store
	statuses *status.Store
	clock    *clock
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	c := &clock{t: base}
	log := slog.New(slog.NewTextHandler(discard{}, nil))

	reg := signal.NewRegistry()
	if err := reg.Add(&signal.Definition{
		Platform: "test", Name: "requests", Kind: signal.KindTimeseries,
		Dims: []string{"url", "account", "user"}, Values: signal.Values{Count: "count", MS: "ms"},
	}); err != nil {
		t.Fatal(err)
	}

	db, err := store.Open(store.Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	silences, err := silence.Open(db.DB(), c.now)
	if err != nil {
		t.Fatal(err)
	}
	statuses, err := status.Open(db.DB(), c.now)
	if err != nil {
		t.Fatal(err)
	}
	alerts := alert.NewStore(alert.Options{Now: c.now})

	brain := core.New(state.NewStore(reg, c.now), reg, log, c.now)
	brain.SetAlerting(core.Alerting{Alerts: alerts, Silences: silences, Statuses: statuses})

	set := rules.Default("test")
	set.Latency.DefaultMS = 1000
	brain.SetRules(set)

	return &fixture{core: brain, alerts: alerts, silences: silences, statuses: statuses, clock: c}
}

func sample(url string, maxMS float64) state.Sample {
	var a state.Agg
	a.Add(1, maxMS, true)
	return state.Sample{
		Platform: "test", Signal: "requests", Minute: state.MinuteOf(base),
		Dims: map[string]string{"url": url, "account": "42"}, Agg: a,
	}
}

func TestSlowRequestRaisesAnAlert(t *testing.T) {
	f := newFixture(t)
	f.core.Samples([]state.Sample{sample("/api/slow", 2500), sample("/api/fast", 50)})

	list := f.alerts.List("test", alert.Filter{})
	if len(list) != 1 {
		t.Fatalf("alerts = %+v, want only the slow one", list)
	}
	got := list[0]
	if got.Category != "latency" || got.Level != protocol.LevelWarn {
		t.Errorf("alert = %+v", got)
	}
	if got.Data["rule"] != "latency/default" {
		t.Errorf("rule = %v", got.Data["rule"])
	}
	if got.Data["limitMs"] != 1000.0 {
		t.Errorf("limit = %v", got.Data["limitMs"])
	}
}

func TestRepeatedSlownessIsOneRow(t *testing.T) {
	f := newFixture(t)
	for i := 0; i < 20; i++ {
		f.core.Samples([]state.Sample{sample("/api/slow", 2500)})
	}
	list := f.alerts.List("test", alert.Filter{})
	if len(list) != 1 {
		t.Fatalf("alerts = %d, want one grouped row", len(list))
	}
	if list[0].Count != 20 {
		t.Errorf("count = %d, want every repeat folded in", list[0].Count)
	}
}

func TestRulesChangeWithoutARestart(t *testing.T) {
	f := newFixture(t)
	f.core.Samples([]state.Sample{sample("/api/slow", 2500)})
	if len(f.alerts.List("test", alert.Filter{})) != 1 {
		t.Fatal("no alert with the original rules")
	}

	// The same traffic stops alerting once the platform allows it.
	set := rules.Default("test")
	set.Latency.DefaultMS = 1000
	set.Latency.Overrides = []rules.Override{{Prefix: "/api/slow", MS: 5000}}
	f.core.SetRules(set)

	f.clock.add(2 * time.Minute)
	f.core.Samples([]state.Sample{sample("/api/slow", 2500)})
	if got := len(f.alerts.List("test", alert.Filter{})); got != 1 {
		t.Errorf("alerts = %d, want no new one after the rule changed", got)
	}
}

func TestSilenceSuppressesWithoutHiding(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.silences.Create(ctx, silence.Silence{
		Platform: "test", Target: "signal:requests", Kind: silence.KindMute,
		Reason: "release in progress", By: "anna",
	}); err != nil {
		t.Fatal(err)
	}

	f.core.Samples([]state.Sample{sample("/api/slow", 2500)})

	if got := f.alerts.List("test", alert.Filter{}); len(got) != 0 {
		t.Errorf("alerts = %+v, want silence to keep them out of the list", got)
	}
	silenced := f.alerts.List("test", alert.Filter{IncludeSilenced: true})
	if len(silenced) != 1 || silenced[0].SilenceReason != "release in progress" {
		t.Fatalf("silenced = %+v, want it visible with its reason", silenced)
	}
	// And the silence records what it has been hiding.
	list := f.silences.List(ctx, "test")
	if len(list) != 1 || list[0].Suppressed == 0 {
		t.Errorf("silence = %+v, want a suppressed count", list)
	}
}

func TestMutedMessagesFromTheRulesFile(t *testing.T) {
	f := newFixture(t)
	set := rules.Default("test")
	set.Mute = []rules.MutePattern{{Contains: "securityService.isLoggedIn"}}
	f.core.SetRules(set)

	f.core.Packets([]protocol.Packet{{
		Header: protocol.Header{V: 1, T: protocol.TypeAlert, Platform: "test"},
		Alert: &protocol.Alert{
			Level: protocol.LevelWarn, Message: "securityService.isLoggedIn returned false",
		},
	}})

	if got := f.alerts.List("test", alert.Filter{}); len(got) != 0 {
		t.Errorf("alerts = %+v, want the muted pattern kept quiet", got)
	}
	if got := f.alerts.List("test", alert.Filter{IncludeSilenced: true}); len(got) != 1 {
		t.Errorf("the muted alert was dropped entirely rather than silenced: %+v", got)
	}
}

func TestOfflineAlertsEveryPlatform(t *testing.T) {
	f := newFixture(t)
	heartbeat := func(platform, key string) protocol.Packet {
		return protocol.Packet{
			Header: protocol.Header{V: 1, T: protocol.TypeStatus, Platform: platform,
				TS: protocol.NewTimestamp(f.clock.now())},
			Status: &protocol.Status{Signal: "servers", Key: key},
		}
	}

	f.core.Packets([]protocol.Packet{heartbeat("test", "web-1"), heartbeat("other", "db-1")})
	f.clock.add(10 * time.Minute)
	f.core.CheckOffline()

	for _, platform := range []string{"test", "other"} {
		if got := f.alerts.List(platform, alert.Filter{}); len(got) != 1 {
			t.Errorf("%s alerts = %+v, want one offline alert", platform, got)
		}
	}
}

func TestOfflineAndRecovery(t *testing.T) {
	f := newFixture(t)
	heartbeat := func() protocol.Packet {
		return protocol.Packet{
			Header: protocol.Header{V: 1, T: protocol.TypeStatus, Platform: "test",
				TS: protocol.NewTimestamp(f.clock.now())},
			Status: &protocol.Status{Signal: "servers", Key: "web-1",
				Payload: map[string]any{"build": "abc"}},
		}
	}

	f.core.Packets([]protocol.Packet{heartbeat()})
	f.core.CheckOffline()
	if got := len(f.alerts.List("test", alert.Filter{})); got != 0 {
		t.Fatalf("alerts = %d while the server is healthy", got)
	}

	// Silent for longer than the platform allows.
	f.clock.add(10 * time.Minute)
	f.core.CheckOffline()
	list := f.alerts.List("test", alert.Filter{})
	if len(list) != 1 || list[0].Level != protocol.LevelError {
		t.Fatalf("alerts = %+v, want an offline error", list)
	}
	if list[0].Target != "status:servers/web-1" {
		t.Errorf("target = %q, want the silenceable id", list[0].Target)
	}

	// Reporting again says so.
	f.clock.add(time.Minute)
	f.core.Packets([]protocol.Packet{heartbeat()})
	list = f.alerts.List("test", alert.Filter{})
	if len(list) != 2 || list[0].Category != "status" {
		t.Fatalf("alerts = %+v, want a recovery", list)
	}
	if list[0].Message != "web-1 is reporting again" {
		t.Errorf("message = %q", list[0].Message)
	}
}

func TestAlertListenersSeeWhatIsRaised(t *testing.T) {
	f := newFixture(t)
	var seen []alert.Alert
	var news []bool
	f.core.OnAlert(func(a alert.Alert, isNew bool) {
		seen = append(seen, a)
		news = append(news, isNew)
	})

	f.core.Samples([]state.Sample{sample("/api/slow", 2500)})
	f.core.Samples([]state.Sample{sample("/api/slow", 2600)})

	if len(seen) != 2 {
		t.Fatalf("listener saw %d alerts", len(seen))
	}
	if !news[0] || news[1] {
		t.Errorf("new flags = %v, want the first new and the second a repeat", news)
	}
}

func TestForgottenServerStopsAlerting(t *testing.T) {
	f := newFixture(t)
	f.core.Packets([]protocol.Packet{{
		Header: protocol.Header{V: 1, T: protocol.TypeStatus, Platform: "test",
			TS: protocol.NewTimestamp(f.clock.now())},
		Status: &protocol.Status{Signal: "servers", Key: "retired-box"},
	}})
	if err := f.statuses.Forget(context.Background(), "test", "servers", "retired-box"); err != nil {
		t.Fatal(err)
	}

	f.clock.add(time.Hour)
	f.core.CheckOffline()
	if got := f.alerts.List("test", alert.Filter{}); len(got) != 0 {
		t.Errorf("a forgotten machine still alerted: %+v", got)
	}
}

func TestASilenceAlsoCoversAlertsRaisedBeforeIt(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.core.Samples([]state.Sample{sample("/api/slow", 2500)})
	f.core.Raise(alert.Alert{Platform: "test", Level: "ERROR", Message: "web-2 has not reported", Target: "status:servers/web-2"})
	var heard []alert.Alert
	f.core.OnAlert(func(a alert.Alert, _ bool) { heard = append(heard, a) })

	if _, err := f.silences.Create(ctx, silence.Silence{
		Platform: "test", Target: "status:servers/web-2", Kind: silence.KindMute, Reason: "maintenance", By: "anna",
	}); err != nil {
		t.Fatal(err)
	}
	if n := f.core.ReapplySilences("test"); n != 1 {
		t.Fatalf("silenced %d, want the one server alert", n)
	}

	visible := f.alerts.List("test", alert.Filter{})
	if len(visible) != 1 || visible[0].Category != "latency" {
		t.Errorf("visible = %+v, want only the latency alert left", visible)
	}
	if len(heard) != 1 || !heard[0].Silenced || heard[0].SilenceReason != "maintenance" {
		t.Errorf("listeners heard %+v, want the change passed on", heard)
	}
	if list := f.silences.List(ctx, "test"); list[0].Suppressed != 1 {
		t.Errorf("suppressed = %d", list[0].Suppressed)
	}

	f.core.Raise(alert.Alert{Platform: "test", Level: "ERROR", Message: "web-2 has not reported", Target: "status:servers/web-2"})
	if got := f.alerts.List("test", alert.Filter{}); len(got) != 1 {
		t.Errorf("a repeat made the silenced row visible again: %+v", got)
	}
}

func TestASilenceOnPartOfAMessageCoversExistingAlerts(t *testing.T) {
	f := newFixture(t)
	f.core.Raise(alert.Alert{Platform: "test", Signal: "alerts", Level: "WARN", Message: "Queue SEND_MAIL is STUCK for 12 min"})
	f.core.Raise(alert.Alert{Platform: "test", Signal: "alerts", Level: "WARN", Message: "Disk almost full"})
	if _, err := f.silences.Create(context.Background(), silence.Silence{
		Platform: "test", Target: `match:contains="send_mail is stuck"`, Kind: silence.KindMute, Reason: "known issue", By: "anna",
	}); err != nil {
		t.Fatal(err)
	}
	if n := f.core.ReapplySilences("test"); n != 1 {
		t.Fatalf("silenced %d, want the stuck-queue alert only", n)
	}
	f.core.Raise(alert.Alert{Platform: "test", Signal: "alerts", Level: "WARN", Message: "Queue SEND_MAIL is stuck for 13 min"})
	visible := f.alerts.List("test", alert.Filter{})
	if len(visible) != 1 || visible[0].Message != "Disk almost full" {
		t.Errorf("visible = %+v, want only the unrelated alert", visible)
	}
}

func TestAlertsShowAgainWhenTheirSilenceIsRemovedOrRunsOut(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.core.Raise(alert.Alert{Platform: "test", Signal: "alerts", Level: "WARN", Message: "queue stuck"})
	f.core.Raise(alert.Alert{Platform: "test", Signal: "alerts", Level: "WARN", Message: "disk full"})
	muted, err := f.silences.Create(ctx, silence.Silence{
		Platform: "test", Target: `match:contains="queue"`, Kind: silence.KindMute, Reason: "known issue", By: "anna",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.silences.Create(ctx, silence.Silence{
		Platform: "test", Target: `match:contains="disk"`, Kind: silence.KindSnooze, Reason: "maintenance", By: "anna",
		Until: f.clock.now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	f.core.ReapplySilences("test")
	if got := f.alerts.List("test", alert.Filter{}); len(got) != 0 {
		t.Fatalf("visible = %+v, want both hidden", got)
	}

	if err := f.silences.Delete(ctx, muted.ID); err != nil {
		t.Fatal(err)
	}
	if n := f.core.ReapplySilences("test"); n != 1 {
		t.Errorf("changed %d, want the queue alert shown again", n)
	}
	f.clock.add(2 * time.Hour)
	f.core.ReapplySilences("test")
	visible := f.alerts.List("test", alert.Filter{})
	if len(visible) != 2 || visible[0].Silenced || visible[0].SilenceReason != "" {
		t.Errorf("visible = %+v, want both back once the snooze ran out", visible)
	}
}

func TestACategoryCanBeSilenced(t *testing.T) {
	f := newFixture(t)
	f.core.Raise(alert.Alert{Platform: "test", Signal: "alerts", Level: "WARN", Category: "Warning JOB", Message: "job slow"})
	f.core.Raise(alert.Alert{Platform: "test", Signal: "alerts", Level: "WARN", Category: "Warning API", Message: "api slow"})
	if _, err := f.silences.Create(context.Background(), silence.Silence{
		Platform: "test", Target: "category:Warning JOB", Kind: silence.KindMute, Reason: "don't need to see it", By: "anna",
	}); err != nil {
		t.Fatal(err)
	}
	f.core.ReapplySilences("test")
	f.core.Raise(alert.Alert{Platform: "test", Signal: "alerts", Level: "WARN", Category: "Warning JOB", Message: "another job slow"})
	visible := f.alerts.List("test", alert.Filter{})
	if len(visible) != 1 || visible[0].Category != "Warning API" {
		t.Errorf("visible = %+v, want only the other category", visible)
	}
}

func TestAHeartbeatCountsWhenItArrivesNotWhenItSaysItWasSent(t *testing.T) {
	f := newFixture(t)
	twoHoursAhead := f.clock.now().Add(2 * time.Hour)
	f.core.Packets([]protocol.Packet{{
		Header: protocol.Header{V: 1, T: protocol.TypeStatus, Platform: "test", TS: protocol.NewTimestamp(twoHoursAhead)},
		Status: &protocol.Status{Signal: "servers", Key: "web-1"},
	}})
	list := f.statuses.List("test")
	if len(list) != 1 || !list[0].LastSeen.Equal(f.clock.now()) {
		t.Fatalf("last seen = %+v, want the moment it arrived", list)
	}

	f.clock.add(6 * time.Minute)
	f.core.CheckOffline()
	if got := f.alerts.List("test", alert.Filter{}); len(got) != 1 || !strings.Contains(got[0].Message, "web-1 has not reported") {
		t.Errorf("alerts = %+v, want the silence noticed after 5 minutes, not after 2 hours", got)
	}
}

func TestAServerStillDownIsRemindedOnTheSameAlert(t *testing.T) {
	f := newFixture(t)
	heartbeat := func() {
		f.core.Packets([]protocol.Packet{{
			Header: protocol.Header{V: 1, T: protocol.TypeStatus, Platform: "test"},
			Status: &protocol.Status{Signal: "servers", Key: "web-1"},
		}})
	}
	var noisy int
	f.core.OnAlert(func(_ alert.Alert, isNew bool) {
		if isNew {
			noisy++
		}
	})
	heartbeat()

	f.clock.add(6 * time.Minute)
	f.core.CheckOffline()
	f.clock.add(2 * time.Minute)
	f.core.CheckOffline()
	if got := f.alerts.List("test", alert.Filter{}); len(got) != 1 || got[0].Count != 1 {
		t.Fatalf("alerts = %+v, want one, not yet reminded", got)
	}

	f.clock.add(3 * time.Minute)
	f.core.CheckOffline()
	f.core.CheckOffline()
	got := f.alerts.List("test", alert.Filter{})
	if len(got) != 1 || got[0].Count != 2 || got[0].Message != "web-1 has not reported for 11m" {
		t.Fatalf("alerts = %+v, want the same row reminded once", got)
	}

	f.clock.add(55 * time.Minute)
	f.core.CheckOffline()
	got = f.alerts.List("test", alert.Filter{})
	if len(got) != 1 || got[0].Message != "web-1 has not reported for 1h6m" {
		t.Errorf("alerts = %+v", got)
	}
	if noisy != 3 {
		t.Errorf("heard %d as new, want the first alert and each reminder", noisy)
	}

	heartbeat()
	f.clock.add(2 * time.Minute)
	f.core.CheckOffline()
	got = f.alerts.List("test", alert.Filter{Categories: []string{"status"}})
	if got[0].Message != "web-1 is reporting again" || got[1].Count != 3 {
		t.Errorf("alerts = %+v, want the recovery on top and the outage row left as it was", got)
	}

	f.clock.add(6 * time.Minute)
	f.core.CheckOffline()
	got = f.alerts.List("test", alert.Filter{Categories: []string{"status"}})
	if got[0].Message != "web-1 has not reported for 5m0s" || got[0].Count != 1 {
		t.Errorf("latest = %+v, want a fresh alert for a new outage", got[0])
	}
}

func TestRemindersCanBeTurnedOff(t *testing.T) {
	f := newFixture(t)
	set := rules.Default("test")
	set.Offline.RepeatSeconds = 0
	f.core.SetRules(set)
	f.core.Packets([]protocol.Packet{{
		Header: protocol.Header{V: 1, T: protocol.TypeStatus, Platform: "test"},
		Status: &protocol.Status{Signal: "servers", Key: "web-1"},
	}})
	for range 5 {
		f.clock.add(6 * time.Minute)
		f.core.CheckOffline()
	}
	if got := f.alerts.List("test", alert.Filter{}); len(got) != 1 || got[0].Count != 1 {
		t.Errorf("alerts = %+v, want the one alert only", got)
	}
}

func TestAGaugeIsFreshFromWhenItArrives(t *testing.T) {
	f := newFixture(t)
	gauges := gauge.NewStore(f.clock.now, time.Minute)
	f.core.SetAlerting(core.Alerting{Alerts: f.alerts, Silences: f.silences, Statuses: f.statuses, Gauges: gauges})
	f.core.Packets([]protocol.Packet{{
		Header: protocol.Header{V: 1, T: protocol.TypeGauge, Platform: "test", TS: protocol.NewTimestamp(f.clock.now().Add(-2 * time.Hour))},
		Gauge:  &protocol.Gauge{Signal: "queues", Points: []protocol.GaugePoint{{Label: "mail", Value: 3}}},
	}})
	v := gauges.View("test", "queues")
	if len(v.Points) != 1 || !v.UpdatedAt.Equal(f.clock.now()) {
		t.Fatalf("view = %+v, want the reading fresh as of its arrival", v)
	}
	f.clock.add(2 * time.Minute)
	if v := gauges.View("test", "queues"); len(v.Points) != 0 {
		t.Errorf("view = %+v, want it gone a TTL after arrival", v)
	}
}
