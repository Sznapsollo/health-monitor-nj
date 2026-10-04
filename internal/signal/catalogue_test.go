package signal_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sznapsollo/health-monitor-nj/internal/signal"
)

func writeCatalogue(t *testing.T, platform, body string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "platforms", platform)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "signals.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadCatalogueAppliesDefaults(t *testing.T) {
	path := writeCatalogue(t, "acme", `
signals:
  requests:
    kind: timeseries
    input:
      dims: [url, user]
`)
	c, err := signal.LoadCatalogue(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.Platform != "acme" {
		t.Errorf("platform = %q, want it from the directory name", c.Platform)
	}
	d := c.Signals["requests"]
	if d == nil {
		t.Fatal("requests is missing")
	}
	if d.Retention.HotDetailMinutes != signal.DefaultHotDetailMinutes ||
		d.Retention.HotTotalsMinutes != signal.DefaultHotTotalsMinutes ||
		d.Retention.DurableDays != signal.DefaultDurableDays {
		t.Errorf("retention = %+v, want the defaults", d.Retention)
	}
	if d.MaxKeys != signal.DefaultMaxKeys {
		t.Errorf("max keys = %d", d.MaxKeys)
	}
	if d.Display.Name != "requests" {
		t.Errorf("display name = %q, want the signal name", d.Display.Name)
	}
	if d.Display.DimName("url") != "url" {
		t.Errorf("dim name = %q, want the identifier as the fallback", d.Display.DimName("url"))
	}
	if d.Values.Count != "count" {
		t.Errorf("count field = %q", d.Values.Count)
	}
}

func TestLoadCatalogueRejectsBadDefinitions(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "unknown kind",
			body: "signals:\n  x:\n    kind: sparkle\n",
			want: "unknown kind",
		},
		{
			name: "duplicate dimension",
			body: "signals:\n  x:\n    kind: timeseries\n    input:\n      dims: [url, url]\n",
			want: "declared twice",
		},
		{
			name: "totals shorter than detail",
			body: "signals:\n  x:\n    kind: timeseries\n    retention: { hot_detail_minutes: 600, hot_totals_minutes: 60 }\n",
			want: "below hot_detail_minutes",
		},
		{
			name: "view without a type",
			body: "signals:\n  x:\n    kind: timeseries\n    views:\n      - id: main\n",
			want: "without a type",
		},
		{
			name: "view series is not a dimension",
			body: "signals:\n  x:\n    kind: timeseries\n    input:\n      dims: [url]\n    views:\n      - id: main\n        type: minuteSeries\n        series: nope\n",
			want: "not a declared dimension",
		},
		{
			name: "keep_pairs names an undeclared dimension",
			body: "signals:\n  x:\n    kind: timeseries\n    input:\n      dims: [url]\n    keep_pairs: [[url, account]]\n",
			want: "two different declared dimensions",
		},
		{
			name: "label names an undeclared dimension",
			body: "signals:\n  x:\n    kind: timeseries\n    input:\n      dims: [account]\n    display:\n      labels: { account: accountName }\n",
			want: "display.labels",
		},
		{
			name: "detail kept longer than anything",
			body: "signals:\n  x:\n    kind: timeseries\n    retention: { durable_days: 2, detail_days: 5 }\n",
			want: "above durable_days",
		},
		{
			name: "gauge sort is not an order",
			body: "signals:\n  x:\n    kind: gauge\n    views:\n      - id: bars\n        type: barGauge\n        options: { sort: loudest }\n",
			want: "is not one of",
		},
		{
			name: "not yaml",
			body: "signals: [this is a list, not a map]\n",
			want: "parse",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := signal.LoadCatalogue(writeCatalogue(t, "acme", tc.body))
			if err == nil {
				t.Fatal("want an error, got none")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestLoadCataloguesMissingDirIsNotAnError(t *testing.T) {
	got, err := signal.LoadCatalogues(filepath.Join(t.TempDir(), "absent"))
	if err != nil || got != nil {
		t.Fatalf("got %v, %v; want nothing and no error", got, err)
	}
}

// The catalogue that ships with the repo has to stay loadable, because it is
// what the legacy adapter feeds.
func TestShippedExampleCatalogue(t *testing.T) {
	cs, err := signal.LoadCatalogues("../../platforms")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	var example *signal.Catalogue
	for _, c := range cs {
		if c.Platform == "example" {
			example = c
		}
	}
	if example == nil {
		t.Fatal("the example catalogue is missing")
	}

	requests := example.Signals["requests"]
	if requests == nil {
		t.Fatal("requests is missing")
	}
	wantDims := []string{"account", "accountName", "url", "port", "user", "ipAddress", "serverName"}
	if len(requests.Dims) != len(wantDims) {
		t.Fatalf("dims = %v, want %v", requests.Dims, wantDims)
	}
	for i, dim := range wantDims {
		if requests.Dims[i] != dim {
			t.Errorf("dim %d = %q, want %q", i, requests.Dims[i], dim)
		}
	}
	if requests.Display.Labels["account"] != "accountName" {
		t.Errorf("labels = %v, want account shown by accountName", requests.Display.Labels)
	}
	if requests.Display.DimName("url") != "URL" {
		t.Errorf("url display name = %q", requests.Display.DimName("url"))
	}
	if len(requests.Views) == 0 {
		t.Error("requests has no views, so nothing would be drawn")
	}

	jobs := example.Signals["jobs"]
	if jobs == nil {
		t.Fatal("jobs is missing")
	}
	if jobs.Kind != signal.KindTimeseries {
		t.Errorf("jobs kind = %q", jobs.Kind)
	}

	queues := example.Signals["jobQueuesLoad"]
	if queues == nil || queues.Kind != signal.KindGauge {
		t.Fatalf("jobQueuesLoad = %+v, want a gauge", queues)
	}
	if queues.TTLSeconds != 120 {
		t.Errorf("ttl_seconds = %d, want the file's 120 to be read", queues.TTLSeconds)
	}
}

func TestAnInfoSignalNamesItsPacketTypeAndKeepsTwentyByDefault(t *testing.T) {
	c, err := signal.LoadCatalogue(writeCatalogue(t, "acme", `
signals:
  jobsList:
    kind: info
    packet_type: configServerJobsListStatus
`))
	if err != nil {
		t.Fatal(err)
	}
	d := c.Signals["jobsList"]
	if d.PacketType != "configServerJobsListStatus" || d.Retention.Versions != 20 || d.Retention.DurableDays != 30 {
		t.Fatalf("got %+v, want the packet type, 20 versions and 30 days", d)
	}

	_, err = signal.LoadCatalogue(writeCatalogue(t, "acme", `
signals:
  jobsList:
    kind: info
`))
	if err == nil || !strings.Contains(err.Error(), "packet_type") {
		t.Fatalf("err = %v, want packet_type asked for", err)
	}
}
