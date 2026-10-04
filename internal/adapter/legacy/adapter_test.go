package legacy_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/adapter/legacy"
	"github.com/Sznapsollo/health-monitor-nj/internal/protocol"
)

// The fixtures are the legacy packets with native names;
// testdata/legacy holds them verbatim, as the senders send them.
const (
	fixtureRequest = `{"type":"request","start":"2026-09-19T10:11:12.123+0200","executionTime":142,
 "message":"","level":"DEBUG","serverName":"prod-1","port":"8080",
 "user":"unknown","account":"unknown","accountName":"unknown",
 "ipAddress":"203.0.113.7","url":"/api/work/groups?day=2026-09-19","groupKey":""}`

	fixtureWarning = `{"type":"customWarning","start":"2026-09-19T10:11:12.123+0200","executionTime":0,
 "message":"Starting serverName: prod-1 appName: example, master, abc1234",
 "level":"INFO","serverName":"prod-1","port":"8080","user":"unknown",
 "account":"unknown","accountName":"unknown","groupKey":""}`

	fixtureStatus = `{"type":"configServerListStatus","start":"2026-09-19T10:11:12.123+0200","executionTime":0,
 "message":"","level":"DEBUG","serverName":"prod-1","port":"8080","user":"unknown",
 "account":"unknown","accountName":"unknown","groupKey":"",
 "startDate":"2026-09-19T10:11:12.123+0200","branch":"master","build":"abc1234",
 "serverConfig":{"host":"prod-1","port":"","branch":"master","build":"abc1234"}}`

	fixtureSendLog = `{"type":"customLog","start":"2026-09-19T10:11:12.123+0200","executionTime":0,
 "message":"","level":"DEBUG","user":"unknown","account":"unknown",
 "accountName":"unknown","groupKey":"","skipRequest":true,
 "logPrefix":"sendLogs","method":"sendMail","emailType":"orderConfirmation",
 "fromMail":"do_not_reply@example.test","fromName":"Example","whichSender":"PRIMARY",
 "to":"user@example.test","subject":"Order confirmation","redirected":false}`

	fixtureJobReport = `{"type":"jobReport","jobName":"example.queue - SEND_MAIL",
 "start":"2026-09-19T10:11:12.123+0200","itemsCount":1,"executionTime":1,
 "user":"user@example.test","account":"user@example.test","accountName":"user@example.test"}`
)

func adaptOne(t *testing.T, a *legacy.Adapter, raw string) protocol.Packet {
	t.Helper()
	got, err := a.Adapt([]byte(raw))
	if err != nil {
		t.Fatalf("adapt: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d packets, want 1", len(got))
	}
	return got[0]
}

func TestRequestBecomesAMetric(t *testing.T) {
	p := adaptOne(t, legacy.New(""), fixtureRequest)

	if p.T != protocol.TypeMetric || p.Metric == nil {
		t.Fatalf("packet = %+v, want a metric", p)
	}
	if p.Platform != "example" {
		t.Errorf("platform = %q", p.Platform)
	}
	if p.Metric.Signal != "requests" {
		t.Errorf("signal = %q", p.Metric.Signal)
	}
	if got := p.Metric.Values["ms"]; got != 142 {
		t.Errorf("ms = %v, want the executionTime", got)
	}
	if got := p.Metric.Values["count"]; got != 1 {
		t.Errorf("count = %v", got)
	}
	want := map[string]string{
		"account": "unknown", "accountName": "unknown", "user": "unknown",
		"url": "/api/work/groups?day=2026-09-19", "port": "8080",
		"ipAddress": "203.0.113.7", "serverName": "prod-1",
	}
	for k, v := range want {
		if p.Metric.Dims[k] != v {
			t.Errorf("dim %s = %q, want %q", k, p.Metric.Dims[k], v)
		}
	}
	// The Java offset has no colon; it still has to parse.
	wantTS := time.Date(2026, 9, 19, 10, 11, 12, 123e6, time.FixedZone("", 7200))
	if !p.TS.Time.Equal(wantTS) {
		t.Errorf("ts = %v, want %v", p.TS.Time, wantTS)
	}
}

func TestJobReportBecomesAMetricCountingItems(t *testing.T) {
	p := adaptOne(t, legacy.New(""), fixtureJobReport)
	if p.Metric == nil || p.Metric.Signal != "jobs" {
		t.Fatalf("packet = %+v, want the jobs metric", p)
	}
	if got := p.Metric.Dims["jobName"]; got != "example.queue - SEND_MAIL" {
		t.Errorf("jobName = %q", got)
	}
	if got := p.Metric.Values["count"]; got != 1 {
		t.Errorf("count = %v, want itemsCount", got)
	}
	// jobs sends no serverName, so the dimension must be absent rather than empty.
	if _, ok := p.Metric.Dims["serverName"]; ok {
		t.Errorf("dims = %+v, want no empty serverName", p.Metric.Dims)
	}
}

func TestJobItemsCountDrivesTheCount(t *testing.T) {
	raw := `{"type":"jobReport","jobName":"q - BULK","start":"2026-09-19T10:11:12.123+0200",
	         "itemsCount":25,"executionTime":900,"account":"a@example.test"}`
	p := adaptOne(t, legacy.New(""), raw)
	if got := p.Metric.Values["count"]; got != 25 {
		t.Errorf("count = %v, want 25", got)
	}
	if got := p.Metric.Values["ms"]; got != 900 {
		t.Errorf("ms = %v, want the report's own duration", got)
	}
}

func TestAccountNameIsLearnedFromRequests(t *testing.T) {
	a := legacy.New("")
	// A request teaches the adapter what account 42 is called.
	_, err := a.Adapt([]byte(`{"type":"request","start":"2026-09-19T10:11:12.123+0200",
	   "executionTime":10,"account":"42","accountName":"Acme Ltd","url":"/x"}`))
	if err != nil {
		t.Fatal(err)
	}
	// A job report for the same account arrives without a name.
	p := adaptOne(t, a, `{"type":"jobReport","jobName":"q - X","itemsCount":1,
	   "start":"2026-09-19T10:11:12.123+0200","account":"42"}`)
	if got := p.Metric.Dims["accountName"]; got != "Acme Ltd" {
		t.Errorf("accountName = %q, want the learned name", got)
	}
}

func TestCustomWarningBecomesAnAlert(t *testing.T) {
	p := adaptOne(t, legacy.New(""), fixtureWarning)
	if p.T != protocol.TypeAlert || p.Alert == nil {
		t.Fatalf("packet = %+v, want an alert", p)
	}
	if p.Alert.Level != protocol.LevelInfo {
		t.Errorf("level = %q", p.Alert.Level)
	}
	if p.Alert.Category != "Info APP" {
		t.Errorf("category = %q, want level and origin", p.Alert.Category)
	}
	if p.Alert.Message == "" {
		t.Error("the message was lost")
	}
}

func TestAlertCategoryFollowsLevelAndOrigin(t *testing.T) {
	tests := []struct {
		level, origin, want string
	}{
		{"ERROR", "", "Error APP"},
		{"WARN", "", "Warning APP"},
		{"INFO", "job", "Info JOB"},
		{"DEBUG", "JOB", "Debug JOB"},
		{"NOTIFY", "", "Notify APP"},
		{"", "", "Info APP"},
	}
	for _, tc := range tests {
		t.Run(tc.level+"/"+tc.origin, func(t *testing.T) {
			raw := `{"type":"customWarning","message":"x","level":"` + tc.level +
				`","origin":"` + tc.origin + `","start":"2026-09-19T10:11:12.123+0200"}`
			p := adaptOne(t, legacy.New(""), raw)
			if p.Alert.Category != tc.want {
				t.Errorf("category = %q, want %q", p.Alert.Category, tc.want)
			}
		})
	}
}

func TestAdditionalDataSurvivesAsAlertData(t *testing.T) {
	raw := `{"type":"customWarning","message":"boom","level":"ERROR",
	  "start":"2026-09-19T10:11:12.123+0200","exception":"NullPointerException",
	  "mailTitle":"Job failed"}`
	p := adaptOne(t, legacy.New(""), raw)
	if p.Alert.Data["exception"] != "NullPointerException" {
		t.Errorf("data = %+v, want the extra keys kept", p.Alert.Data)
	}
	if _, repeated := p.Alert.Data["message"]; repeated {
		t.Errorf("data = %+v, want no duplicate of a field the envelope already has", p.Alert.Data)
	}
}

func TestSendLogBecomesALog(t *testing.T) {
	p := adaptOne(t, legacy.New(""), fixtureSendLog)
	if p.T != protocol.TypeLog || p.Log == nil {
		t.Fatalf("packet = %+v, want a log", p)
	}
	if p.Log.Signal != "sendLogs" {
		t.Errorf("signal = %q, want the logPrefix", p.Log.Signal)
	}
	if p.Log.Data["to"] != "user@example.test" || p.Log.Data["subject"] != "Order confirmation" {
		t.Errorf("data = %+v, want the mail fields", p.Log.Data)
	}
	// A log is searched by account and user as often as by its own fields, so
	// those have to travel with the row rather than being stripped as
	// "already in the envelope".
	if p.Log.Data["account"] != "unknown" || p.Log.Data["user"] != "unknown" {
		t.Errorf("data = %+v, want the searchable identity kept", p.Log.Data)
	}
	if p.Log.Level != protocol.LevelDebug {
		t.Errorf("level = %q", p.Log.Level)
	}
}

func TestServerStatusBecomesAStatus(t *testing.T) {
	p := adaptOne(t, legacy.New(""), fixtureStatus)
	if p.T != protocol.TypeStatus || p.Status == nil {
		t.Fatalf("packet = %+v, want a status", p)
	}
	if p.Status.Key != "prod-1" {
		t.Errorf("key = %q, want the server name", p.Status.Key)
	}
	if p.Status.Payload["branch"] != "master" || p.Status.Payload["build"] != "abc1234" {
		t.Errorf("payload = %+v", p.Status.Payload)
	}
	cfg, ok := p.Status.Payload["serverConfig"].(map[string]any)
	if !ok || cfg["host"] != "prod-1" {
		t.Errorf("serverConfig = %+v", p.Status.Payload["serverConfig"])
	}
}

func TestSenderOverridesAreHonoured(t *testing.T) {
	// additionalData is merged into the root, so a sender can override `type`
	// with anything; an unknown type is free text with a level, not a drop.
	p := adaptOne(t, legacy.New(""), `{"type":"somethingNew","message":"hello",
	  "level":"WARN","start":"2026-09-19T10:11:12.123+0200"}`)
	if p.Alert == nil || p.Alert.Message != "hello" {
		t.Fatalf("packet = %+v, want it kept as an alert", p)
	}
	// A packet with no type at all is what the senders' default builder sends.
	p = adaptOne(t, legacy.New(""), `{"message":"default","level":"ERROR",
	  "start":"2026-09-19T10:11:12.123+0200"}`)
	if p.Alert == nil || p.Alert.Level != protocol.LevelError {
		t.Fatalf("packet = %+v, want an error alert", p)
	}
}

func TestBadJSONIsAnError(t *testing.T) {
	if _, err := legacy.New("").Adapt([]byte("not json")); err == nil {
		t.Fatal("want an error")
	}
}

func TestPlatformOverride(t *testing.T) {
	p := adaptOne(t, legacy.New("mojegodzinki"), fixtureRequest)
	if p.Platform != "mojegodzinki" {
		t.Errorf("platform = %q", p.Platform)
	}
}

func BenchmarkAdaptRequest(b *testing.B) {
	a := legacy.New("")
	raw := []byte(fixtureRequest)
	b.SetBytes(int64(len(raw)))
	for i := 0; i < b.N; i++ {
		if _, err := a.Adapt(raw); err != nil {
			b.Fatal(err)
		}
	}
}

func TestDailyLogsAreOptional(t *testing.T) {
	// Off by default: a high-volume platform pays only for the charts.
	got, err := legacy.New("").Adapt([]byte(fixtureRequest))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("packets = %d, want only the metric", len(got))
	}

	on := legacy.NewWithOptions(legacy.Options{DailyLogs: true})
	got, err = on.Adapt([]byte(fixtureRequest))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("packets = %d, want the metric and the searchable row", len(got))
	}
	if got[0].Metric == nil || got[1].Log == nil {
		t.Fatalf("packets = %+v", got)
	}
	row := got[1].Log
	if row.Signal != "dailyLogs" {
		t.Errorf("signal = %q", row.Signal)
	}
	if row.Data["url"] != "/api/work/groups?day=2026-09-19" {
		t.Errorf("url = %v", row.Data["url"])
	}
	if row.Data["executionTime"] != 142.0 {
		t.Errorf("executionTime = %v", row.Data["executionTime"])
	}
	if row.Data["ipAddress"] != "203.0.113.7" {
		t.Errorf("ipAddress = %v", row.Data["ipAddress"])
	}
}

// The shapes below are read straight from the old server
// (Server.groovy:2379-2409), which is the only specification these packets
// have.
func TestQueueLoadBecomesAGauge(t *testing.T) {
	raw := `{"type":"jobQueuesLoad","start":"2026-09-19T10:11:12.123+0200","serverName":"jobs-1",
	  "queueLoads":[{"name":"example.queue","value":142,"warning":false},
	                {"name":"SEND_MAIL","value":7,"warning":true}]}`
	p := adaptOne(t, legacy.New(""), raw)

	if p.T != protocol.TypeGauge || p.Gauge == nil {
		t.Fatalf("packet = %+v, want a gauge", p)
	}
	if p.Gauge.Signal != "jobQueuesLoad" {
		t.Errorf("signal = %q", p.Gauge.Signal)
	}
	if p.Source != "jobs-1" {
		t.Errorf("source = %q, want the reporting server", p.Source)
	}
	if len(p.Gauge.Points) != 2 {
		t.Fatalf("points = %+v", p.Gauge.Points)
	}
	if p.Gauge.Points[0].Label != "example.queue" || p.Gauge.Points[0].Value != 142 {
		t.Errorf("first point = %+v", p.Gauge.Points[0])
	}
	if !p.Gauge.Points[1].Warn {
		t.Errorf("the warning flag was lost: %+v", p.Gauge.Points[1])
	}
}

func TestVPNUsersBecomesAGaugeWithItsOwnSource(t *testing.T) {
	raw := `{"type":"vpnUsersLoad","start":"2026-09-19T10:11:12.123+0200","source":"gw-2",
	  "vpnUsers":[{"name":"office","value":4,"warning":false}]}`
	p := adaptOne(t, legacy.New(""), raw)

	if p.Gauge == nil || p.Gauge.Signal != "vpnUsersLoad" {
		t.Fatalf("packet = %+v", p)
	}
	// vpnUsersLoad carries its own source, which is how several gateways
	// merge into one chart.
	if p.Source != "gw-2" {
		t.Errorf("source = %q", p.Source)
	}
}

func TestAnEmptyQueueListIsValidData(t *testing.T) {
	// "Nothing is queued" is a real reading, not a missing one: it has to
	// reach the chart, or the last non-empty value would stay on screen.
	raw := `{"type":"jobQueuesLoad","start":"2026-09-19T10:11:12.123+0200","serverName":"jobs-1","queueLoads":[]}`
	p := adaptOne(t, legacy.New(""), raw)
	if p.Gauge == nil {
		t.Fatalf("packet = %+v, want a gauge", p)
	}
	if len(p.Gauge.Points) != 0 {
		t.Errorf("points = %+v, want none", p.Gauge.Points)
	}
}

func TestQueueLoadWithoutAServerNameStillHasASource(t *testing.T) {
	raw := `{"type":"jobQueuesLoad","start":"2026-09-19T10:11:12.123+0200","queueLoads":[{"name":"q","value":1}]}`
	p := adaptOne(t, legacy.New(""), raw)
	if p.Source != "default" {
		t.Errorf("source = %q, want a fallback rather than an empty key", p.Source)
	}
}

func TestAPacketTypeAnInfoSignalNamesIsPassedOnWhole(t *testing.T) {
	a := legacy.NewWithOptions(legacy.Options{
		Mapping: &legacy.Mapping{Types: map[string]string{"configServerJobsListStatus": "configServerListStatus"}},
		InfoSignal: func(types ...string) string {
			if slices.Contains(types, "configServerJobsListStatus") {
				return "jobsList"
			}
			return ""
		},
	})
	p := adaptOne(t, a, `{"type":"configServerJobsListStatus","start":"2026-09-26T11:15:44.372+0200","serverName":"config-1",
		"jobsStatusMap":{"saveDocument":{"name":"saveDocument","heartBeat":1790413801933}}}`)
	if p.Status == nil || p.Status.Signal != "jobsList" || p.Status.Key != "config-1" {
		t.Fatalf("got %+v, want a jobsList status keyed by the sender", p)
	}
	jobs, _ := p.Status.Content["jobsStatusMap"].(map[string]any)
	if _, ok := jobs["saveDocument"]; !ok {
		t.Fatalf("content = %v, want the whole packet", p.Status.Content)
	}

	other := adaptOne(t, a, `{"type":"configServerListStatus","serverName":"web-1","start":"2026-09-26T11:15:44.372+0200"}`)
	if other.Status == nil || other.Status.Signal != legacy.SignalServers {
		t.Fatalf("got %+v, want other types handled as before", other)
	}
}

func TestAnUnknownPacketTypeIsStillAnAlertAndIsReported(t *testing.T) {
	var heard []string
	var last []byte
	a := legacy.NewWithOptions(legacy.Options{OnUnknownType: func(packetType string, raw []byte) {
		heard = append(heard, packetType)
		last = raw
	}})
	p := adaptOne(t, a, `{"type":"serverMemReport","start":"2026-09-26T10:20:44.250+0200","serverMemName":"8080","serverMemUsage":{}}`)
	if p.Alert == nil {
		t.Fatalf("got %+v, want it still read as an alert", p)
	}
	_ = adaptOne(t, a, `{"type":"customWarning","start":"2026-09-26T10:20:44.250+0200","message":"boom"}`)
	if !slices.Equal(heard, []string{"serverMemReport"}) {
		t.Fatalf("heard %v, want only the invented type", heard)
	}
	if !strings.Contains(string(last), `"serverMemName":"8080"`) {
		t.Fatalf("packet = %s, want it as it arrived", last)
	}
}

func TestAPacketTypeATimeseriesNamesBecomesAMetric(t *testing.T) {
	var heard []string
	a := legacy.NewWithOptions(legacy.Options{
		MetricSignal: func(types ...string) string {
			if slices.Contains(types, "ksefEventStatusData") {
				return "ksefEvents"
			}
			return ""
		},
		OnUnknownType: func(packetType string, _ []byte) { heard = append(heard, packetType) },
	})
	p := adaptOne(t, a, `{"type":"ksefEventStatusData","start":"2026-09-29T14:55:46.875+0200","account":"624ee1d3",
		"executionTime":1,"itemsCount":3,"taskName":"DOWNLOAD_DOCUMENT_PACKAGE","taskStatus":"COMPLETED"}`)
	if p.Metric == nil || p.Metric.Signal != "ksefEvents" {
		t.Fatalf("got %+v, want a ksefEvents metric", p)
	}
	if p.Metric.Dims["taskStatus"] != "COMPLETED" || p.Metric.Dims["account"] != "624ee1d3" {
		t.Errorf("dims = %v", p.Metric.Dims)
	}
	if p.Metric.Values["itemsCount"] != 3 || p.Metric.Values["executionTime"] != 1 {
		t.Errorf("values = %v", p.Metric.Values)
	}
	if len(heard) != 0 {
		t.Errorf("heard %v, want a charted type not reported as unknown", heard)
	}
}
