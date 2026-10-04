package signal_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sznapsollo/health-monitor-nj/internal/signal"
)

func TestDesignedSignalsRoundTrip(t *testing.T) {
	dir := filepath.Dir(filepath.Dir(writeCatalogue(t, "acme", `
signals:
  requests:
    kind: timeseries
    input: { dims: [url] }
`)))

	made := signal.Definition{
		Platform: "acme", Name: "checkout", Kind: signal.KindTimeseries,
		Dims:   []string{"region", "url"},
		Values: signal.Values{Count: "count", MS: "ms"},
		Display: signal.Display{Name: "Checkout", Dims: map[string]string{"region": "Region"},
			Colors: map[string]string{"COMPLETED": "#2e7d32"}},
		Retention: signal.Retention{HotDetailMinutes: 60, HotTotalsMinutes: 600, DurableDays: 7, DetailDays: 2},
		KeepPairs: [][2]string{{"region", "url"}},
		Views:     []signal.View{{ID: "all", Type: "minuteSeries", Value: "count", DefaultOn: true}},
		CreatedBy: "anna", UpdatedBy: "bob",
	}
	if err := signal.Save(dir, made); err != nil {
		t.Fatal(err)
	}
	c, err := signal.LoadPlatform(dir, "acme")
	if err != nil {
		t.Fatal(err)
	}
	if !c.Signals["requests"].ReadOnly {
		t.Error("the shipped signal is not read-only")
	}
	got := c.Signals["checkout"]
	if got == nil || got.ReadOnly {
		t.Fatalf("checkout = %+v", got)
	}
	if strings.Join(got.Dims, ",") != "region,url" || got.Values != made.Values ||
		got.Retention != made.Retention || got.Display.Name != "Checkout" ||
		got.Display.DimName("region") != "Region" || got.Display.Colors["COMPLETED"] != "#2e7d32" || got.CreatedBy != "anna" || got.UpdatedBy != "bob" ||
		len(got.Views) != 1 || got.Views[0].ID != "all" ||
		len(got.KeepPairs) != 1 || got.KeepPairs[0] != [2]string{"region", "url"} {
		t.Errorf("checkout came back as %+v", got)
	}

	if err := signal.Delete(dir, "acme", "checkout"); err != nil {
		t.Fatal(err)
	}
	if c, err = signal.LoadPlatform(dir, "acme"); err != nil || c.Signals["checkout"] != nil {
		t.Errorf("after delete: %v, %v", c.Signals["checkout"], err)
	}
}

func TestAnInfoSignalKeptOffStatusRoundTrips(t *testing.T) {
	dir := filepath.Dir(filepath.Dir(writeCatalogue(t, "acme", "signals: {}\n")))
	made := signal.Definition{
		Platform: "acme", Name: "dailyReport", Kind: signal.KindInfo,
		PacketType: "godzinkiDailyReportStatus", NoStatus: true,
	}
	if err := signal.Save(dir, made); err != nil {
		t.Fatal(err)
	}
	c, err := signal.LoadPlatform(dir, "acme")
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Signals["dailyReport"]; got == nil || !got.NoStatus {
		t.Fatalf("dailyReport = %+v, want it kept off Status", got)
	}
}

func TestDesignedPlatformNeedsNoShippedFile(t *testing.T) {
	dir := t.TempDir()
	if err := signal.Save(dir, signal.Definition{Platform: "demo", Name: "disk", Kind: signal.KindGauge}); err != nil {
		t.Fatal(err)
	}
	all, err := signal.LoadCatalogues(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].Platform != "demo" || all[0].Signals["disk"] == nil {
		t.Errorf("catalogues = %+v", all)
	}
}

func TestDesignedNameClashIsRefused(t *testing.T) {
	path := writeCatalogue(t, "acme", "signals:\n  requests:\n    kind: timeseries\n")
	dir := filepath.Dir(filepath.Dir(path))
	folder := filepath.Join(dir, "acme", signal.DesignedDir)
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "requests.yaml"), []byte("kind: timeseries\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := signal.LoadPlatform(dir, "acme"); err == nil {
		t.Error("a signal in both places was accepted")
	}
}

func TestSaveRefusesUnsafeNames(t *testing.T) {
	dir := t.TempDir()
	for _, d := range []signal.Definition{
		{Platform: "../etc", Name: "x", Kind: signal.KindGauge},
		{Platform: "demo", Name: "a/b", Kind: signal.KindGauge},
		{Platform: "demo", Name: ".hidden", Kind: signal.KindGauge},
	} {
		if err := signal.Save(dir, d); err == nil {
			t.Errorf("saved %s/%s", d.Platform, d.Name)
		}
	}
}

func TestReloadDropsAnEmptiedPlatform(t *testing.T) {
	dir := t.TempDir()
	reg := signal.NewRegistry()
	if err := signal.Save(dir, signal.Definition{Platform: "demo", Name: "disk", Kind: signal.KindGauge}); err != nil {
		t.Fatal(err)
	}
	if _, err := signal.Reload(dir, "demo", reg); err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Lookup("demo", "disk"); !ok {
		t.Fatal("disk not registered")
	}
	if err := signal.Delete(dir, "demo", "disk"); err != nil {
		t.Fatal(err)
	}
	if _, err := signal.Reload(dir, "demo", reg); err != nil {
		t.Fatal(err)
	}
	if len(reg.Platforms()) != 0 {
		t.Errorf("platforms = %v, want none", reg.Platforms())
	}
}

func TestAColourMustBeAHexColour(t *testing.T) {
	d := signal.Definition{
		Platform: "acme", Name: "tasks", Kind: signal.KindTimeseries,
		Display: signal.Display{Colors: map[string]string{"COMPLETED": "green"}},
	}
	if err := signal.Save(t.TempDir(), d); err == nil || !strings.Contains(err.Error(), "colors") {
		t.Fatalf("err = %v, want the colour refused", err)
	}
}
