package signal_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Sznapsollo/health-monitor-nj/internal/signal"
)

func TestExportedSignalsImportAsTheyWere(t *testing.T) {
	cat, err := signal.LoadCatalogue(filepath.Join("..", "..", "platforms", "example", "signals.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var defs []*signal.Definition
	for _, d := range cat.Signals {
		defs = append(defs, d)
	}
	defs = append(defs, signal.PacketsDefinition("example"), &signal.Definition{
		Platform: "example", Name: "guessed", Kind: signal.KindTimeseries, AutoRegistered: true,
	})

	b, err := signal.Export("example", defs)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "packets") || strings.Contains(string(b), "guessed") {
		t.Errorf("built-in or auto-registered signals were exported:\n%s", b)
	}

	back, err := signal.ParseExport(b, "other")
	if err != nil {
		t.Fatal(err)
	}
	if len(back) != len(cat.Signals) {
		t.Fatalf("imported %d signals, exported %d", len(back), len(cat.Signals))
	}
	for _, d := range back {
		was := cat.Signals[d.Name]
		if d.Platform != "other" || d.ReadOnly || d.Kind != was.Kind ||
			!reflect.DeepEqual(d.Views, was.Views) || !reflect.DeepEqual(d.Dims, was.Dims) {
			t.Errorf("%s came back as %+v, was %+v", d.Name, d, was)
		}
	}
}

func TestAnyPlatformsSignalsYamlCanBeImported(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "platforms", "example", "signals.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := signal.ParseExport(b, "other"); err != nil {
		t.Fatal(err)
	}
}

func TestABrokenSignalRefusesTheWholeFile(t *testing.T) {
	for name, text := range map[string]string{
		"not yaml":  "signals: [",
		"empty":     "platform: x\n",
		"bad kind":  "signals:\n  ok:\n    kind: gauge\n  audit:\n    kind: status\n",
		"bad name":  "signals:\n  ../x:\n    kind: gauge\n",
		"bad views": "signals:\n  x:\n    kind: timeseries\n    input: { dims: [a, a] }\n",
	} {
		if _, err := signal.ParseExport([]byte(text), "p"); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
