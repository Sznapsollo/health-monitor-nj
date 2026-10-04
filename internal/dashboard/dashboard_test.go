package dashboard_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sznapsollo/health-monitor-nj/internal/dashboard"
)

// The arrangement from the request that prompted this: queues above requests
// on the left, alerts down the right.
const example = `
dashboards:
  ops:
    name: "Operacje"
    default: true
    columns:
      - width: 2
        panels:
          - type: gauge
            signal: jobQueuesLoad
            height: 220
          - type: chart
            signal: requests
            group: url
            top: 5
      - width: 1
        panels:
          - type: alerts
            levels: [ERROR, WARN]
            limit: 20
          - type: status
  quiet:
    name: "Tylko wykresy"
    columns:
      - panels:
          - type: chart
            signal: requests
`

func write(t *testing.T, platform, body string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), platform)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "dashboards.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadExample(t *testing.T) {
	got, err := dashboard.Load(write(t, "example", example))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("dashboards = %d", len(got))
	}

	// The default comes first, so a picker opens on it.
	ops := got[0]
	if ops.ID != "ops" || !ops.Default || ops.Name != "Operacje" {
		t.Fatalf("first = %+v", ops)
	}
	if ops.Platform != "example" {
		t.Errorf("platform = %q, want it from the directory", ops.Platform)
	}
	if len(ops.Rows) != 1 || len(ops.Rows[0].Columns) != 2 || ops.Columns != nil {
		t.Fatalf("rows = %+v, want the columns folded into one row", ops.Rows)
	}
	columns := ops.Rows[0].Columns
	if columns[0].Width != 2 || columns[1].Width != 1 {
		t.Errorf("widths = %d and %d", columns[0].Width, columns[1].Width)
	}

	left := columns[0].Panels
	if left[0].Type != dashboard.PanelGauge || left[0].Signal != "jobQueuesLoad" {
		t.Errorf("top left = %+v, want the queues", left[0])
	}
	if left[1].Type != dashboard.PanelChart || left[1].Group != "url" || left[1].Top != 5 {
		t.Errorf("below it = %+v, want requests by url", left[1])
	}
	right := columns[1].Panels
	if right[0].Type != dashboard.PanelAlerts || len(right[0].Levels) != 2 {
		t.Errorf("top right = %+v, want the alerts", right[0])
	}

	if signals := ops.Signals(); len(signals) != 1 || signals[0] != "requests" {
		t.Errorf("signals = %v, want what the charts need", signals)
	}
}

func TestMissingFileIsNotAnError(t *testing.T) {
	got, err := dashboard.Load(filepath.Join(t.TempDir(), "acme", "dashboards.yaml"))
	if err != nil || got != nil {
		t.Fatalf("got %v, %v; want nothing and no error", got, err)
	}
}

func TestValidationRejectsWhatCannotBeDrawn(t *testing.T) {
	tests := []struct {
		name, body, want string
	}{
		{
			name: "no rows",
			body: "dashboards:\n  x:\n    name: X\n",
			want: "no rows",
		},
		{
			name: "empty column",
			body: "dashboards:\n  x:\n    columns:\n      - panels: []\n",
			want: "no panels",
		},
		{
			name: "unknown panel",
			body: "dashboards:\n  x:\n    columns:\n      - panels:\n          - type: hologram\n",
			want: "unknown type",
		},
		{
			name: "chart without a signal",
			body: "dashboards:\n  x:\n    columns:\n      - panels:\n          - type: chart\n",
			want: "needs a signal",
		},
		{
			name: "sub without a group",
			body: "dashboards:\n  x:\n    columns:\n      - panels:\n          - type: chart\n            signal: requests\n            sub: user\n",
			want: "sub-grouping needs a grouping",
		},
		{
			name: "not a level",
			body: "dashboards:\n  x:\n    columns:\n      - panels:\n          - type: alerts\n            levels: [LOUD]\n",
			want: "is not an alert level",
		},
		{
			name: "not a sort",
			body: "dashboards:\n  x:\n    columns:\n      - panels:\n          - type: gauge\n            signal: q\n            sort: loudest\n",
			want: "is not one of",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := dashboard.Load(write(t, "acme", tc.body))
			if err == nil {
				t.Fatal("want an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestStore(t *testing.T) {
	s := dashboard.NewStore()
	loaded, err := dashboard.Load(write(t, "example", example))
	if err != nil {
		t.Fatal(err)
	}
	s.Replace("example", loaded)

	if got := s.For("example"); len(got) != 2 {
		t.Fatalf("stored = %+v", got)
	}
	if got := s.For("other"); len(got) != 0 {
		t.Errorf("another platform sees %+v", got)
	}

	// Emptying a file removes the arrangement rather than leaving a ghost.
	s.Replace("example", nil)
	if got := s.For("example"); len(got) != 0 {
		t.Errorf("after removal: %+v", got)
	}
}

func TestSharedDashboardsLiveOnePerFile(t *testing.T) {
	root := t.TempDir()
	write := func(platform, body string) {
		dir := filepath.Join(root, platform)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "dashboards.yaml"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("example", example)

	made := dashboard.Dashboard{
		ID: "anna-ops", Platform: "example", Name: "Anna's ops", CreatedBy: "anna",
		Rows: []dashboard.Row{
			{Columns: []dashboard.Column{{Panels: []dashboard.Panel{{Type: dashboard.PanelStatus}}}}},
			{Columns: []dashboard.Column{{Width: 2, Panels: []dashboard.Panel{{Type: dashboard.PanelAlerts}}}}},
		},
	}
	if err := dashboard.Save(root, made); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "example", "dashboards", "anna-ops.yaml")); err != nil {
		t.Fatal(err)
	}

	got, err := dashboard.LoadPlatform(root, "example")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("dashboards = %d, want the shipped two plus the made one", len(got))
	}
	var shipped, own *dashboard.Dashboard
	for i := range got {
		switch got[i].ID {
		case "ops":
			shipped = &got[i]
		case "anna-ops":
			own = &got[i]
		}
	}
	if shipped == nil || !shipped.ReadOnly {
		t.Errorf("shipped = %+v, want it read-only", shipped)
	}
	if own == nil || own.ReadOnly || own.CreatedBy != "anna" || own.Name != "Anna's ops" {
		t.Errorf("made = %+v, want it editable and attributed", own)
	}
	if own != nil && (len(own.Rows) != 2 || own.Rows[1].Columns[0].Width != 2) {
		t.Errorf("rows = %+v, want two rows back", own.Rows)
	}

	if err := dashboard.Save(root, dashboard.Dashboard{ID: "../x", Platform: "example",
		Rows: made.Rows}); err == nil {
		t.Error("an id that is a path was accepted")
	}

	if err := dashboard.Delete(root, "example", "anna-ops"); err != nil {
		t.Fatal(err)
	}
	got, err = dashboard.LoadPlatform(root, "example")
	if err != nil || len(got) != 2 {
		t.Fatalf("after delete = %d, %v", len(got), err)
	}

	// An id in both places is a mistake worth refusing, not shadowing.
	if err := dashboard.Save(root, dashboard.Dashboard{ID: "ops", Platform: "example",
		Rows: made.Rows}); err != nil {
		t.Fatal(err)
	}
	if _, err := dashboard.LoadPlatform(root, "example"); err == nil ||
		!strings.Contains(err.Error(), "both") {
		t.Errorf("err = %v, want a complaint about the duplicate id", err)
	}
}

func TestStackedChoiceSurvivesSave(t *testing.T) {
	root := t.TempDir()
	off := false
	made := dashboard.Dashboard{
		ID: "ports", Platform: "example", Name: "Ports",
		Rows: []dashboard.Row{{Columns: []dashboard.Column{{Panels: []dashboard.Panel{
			{Type: dashboard.PanelChart, Signal: "requests", Group: "port", Sub: "account", Stacked: &off},
			{Type: dashboard.PanelChart, Signal: "requests", Group: "port", Sub: "account"},
		}}}}},
	}
	if err := dashboard.Save(root, made); err != nil {
		t.Fatal(err)
	}
	got, err := dashboard.LoadPlatform(root, "example")
	if err != nil {
		t.Fatal(err)
	}
	panels := got[0].Rows[0].Columns[0].Panels
	if panels[0].Stacked == nil || *panels[0].Stacked {
		t.Errorf("side-by-side panel came back as %v", panels[0].Stacked)
	}
	if panels[1].Stacked != nil {
		t.Errorf("default panel came back as %v, want unset", *panels[1].Stacked)
	}
}
