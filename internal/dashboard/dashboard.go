// Package dashboard is the arrangement of panels a team wants to look at:
// which charts, which alerts, side by side and in what order. Like signals and
// rules it is configuration, not code — one file per platform, hot-reloaded,
// shared by everyone including the wall display.
package dashboard

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/Sznapsollo/health-monitor-nj/internal/signal"
)

// PanelType is what a panel draws.
type PanelType string

const (
	// PanelChart is a signal's per-minute chart.
	PanelChart PanelType = "chart"
	// PanelGauge is the latest labelled values of a gauge signal.
	PanelGauge PanelType = "gauge"
	// PanelAlerts is the alert list, optionally narrowed.
	PanelAlerts PanelType = "alerts"
	// PanelStatus is what reports its state, and what has gone quiet.
	PanelStatus PanelType = "status"
)

// Valid reports whether the server knows how to draw this.
func (p PanelType) Valid() bool {
	switch p {
	case PanelChart, PanelGauge, PanelAlerts, PanelStatus:
		return true
	}
	return false
}

// Panel is one box on a dashboard.
type Panel struct {
	Type   PanelType `json:"type" yaml:"type"`
	Title  string    `json:"title,omitempty" yaml:"title,omitempty"`
	Signal string    `json:"signal,omitempty" yaml:"signal,omitempty"`
	Height int       `json:"height,omitempty" yaml:"height,omitempty"`

	// Chart panels: how the signal should be broken down while this
	// dashboard is open.
	Group string `json:"group,omitempty" yaml:"group,omitempty"`
	Sub   string `json:"sub,omitempty" yaml:"sub,omitempty"`
	// Filter and SubFilter keep only matching values, as on the Charts tab.
	Filter    string `json:"filter,omitempty" yaml:"filter,omitempty"`
	SubFilter string `json:"subFilter,omitempty" yaml:"sub_filter,omitempty"`
	Top       int    `json:"top,omitempty" yaml:"top,omitempty"`
	Minutes   int    `json:"minutes,omitempty" yaml:"minutes,omitempty"`
	// GroupMinutes is the group tiles' own window; 0 means 120.
	GroupMinutes int `json:"groupMinutes,omitempty" yaml:"group_minutes,omitempty"`
	// Stacked defaults to true when Sub is set; false draws the sub-groups side by side.
	Stacked *bool `json:"stacked,omitempty" yaml:"stacked,omitempty"`
	// Main false hides the main chart and keeps only the group tiles; needs Group.
	Main *bool `json:"main,omitempty" yaml:"main,omitempty"`
	// Together draws every group in one stacked chart instead of a tile each.
	Together bool `json:"together,omitempty" yaml:"together,omitempty"`

	// Gauge panels: label, labelDesc, value or valueDesc; empty keeps the signal's own.
	Sort string `json:"sort,omitempty" yaml:"sort,omitempty"`

	// Alert panels.
	Levels     []string `json:"levels,omitempty" yaml:"levels,omitempty"`
	Categories []string `json:"categories,omitempty" yaml:"categories,omitempty"`
	Limit      int      `json:"limit,omitempty" yaml:"limit,omitempty"`
}

// Column is a vertical stack of panels.
type Column struct {
	// Width is relative: two columns of 2 and 1 split the screen two thirds
	// to one third.
	Width  int     `json:"width,omitempty" yaml:"width,omitempty"`
	Panels []Panel `json:"panels" yaml:"panels"`
}

// Row is columns side by side; rows stack.
type Row struct {
	Columns []Column `json:"columns" yaml:"columns"`
}

// Dashboard is one named arrangement.
type Dashboard struct {
	ID       string `json:"id" yaml:"-"`
	Platform string `json:"platform" yaml:"-"`
	Name     string `json:"name" yaml:"name"`
	Default  bool   `json:"default,omitempty" yaml:"default,omitempty"`
	Rows     []Row  `json:"rows" yaml:"rows,omitempty"`
	// Columns is the older single-row form; normalise folds it into Rows.
	Columns []Column `json:"columns,omitempty" yaml:"columns,omitempty"`
	// CreatedBy and UpdatedBy are the login names; attribution, not permission.
	CreatedBy string `json:"createdBy,omitempty" yaml:"created_by,omitempty"`
	UpdatedBy string `json:"updatedBy,omitempty" yaml:"updated_by,omitempty"`
	// ReadOnly marks a dashboard from the shipped dashboards.yaml; the UI
	// duplicates those rather than editing them in place.
	ReadOnly bool `json:"readOnly,omitempty" yaml:"-"`
}

// SharedDir is where dashboards made from the UI live, one file each.
const SharedDir = "dashboards"

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// ValidID reports whether an id can name a file and a URL segment.
func ValidID(id string) error {
	if !idPattern.MatchString(id) {
		return fmt.Errorf("dashboard id %q: use lowercase letters, digits, - and _", id)
	}
	return nil
}

// Signals lists the signals this dashboard's charts need, so opening it can
// ask the server for exactly those.
func (d Dashboard) Signals() []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range d.Rows {
		for _, c := range r.Columns {
			for _, p := range c.Panels {
				if p.Type == PanelChart && p.Signal != "" && !seen[p.Signal] {
					seen[p.Signal] = true
					out = append(out, p.Signal)
				}
			}
		}
	}
	return out
}

// Normalise folds the single-row form into Rows.
func (d *Dashboard) Normalise() {
	if len(d.Rows) == 0 && len(d.Columns) > 0 {
		d.Rows = []Row{{Columns: d.Columns}}
	}
	d.Columns = nil
}

type yamlFile struct {
	Platform   string               `yaml:"platform"`
	Dashboards map[string]Dashboard `yaml:"dashboards"`
}

// Load reads one platform's dashboards. A missing file is not an error: a
// platform without one simply has no arranged view yet.
func Load(path string) ([]Dashboard, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("dashboard: read %s: %w", path, err)
	}

	var raw yamlFile
	if err := yaml.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("dashboard: parse %s: %w", path, err)
	}
	platform := raw.Platform
	if platform == "" {
		platform = filepath.Base(filepath.Dir(path))
	}

	out := make([]Dashboard, 0, len(raw.Dashboards))
	for id, d := range raw.Dashboards {
		d.ID = id
		d.Platform = platform
		d.ReadOnly = true
		d.Normalise()
		if d.Name == "" {
			d.Name = id
		}
		if err := d.Validate(); err != nil {
			return nil, fmt.Errorf("dashboard: %s: %w", path, err)
		}
		out = append(out, d)
	}
	sortDashboards(out)
	return out, nil
}

func sortDashboards(out []Dashboard) {
	sort.Slice(out, func(i, j int) bool {
		if out[i].Default != out[j].Default {
			return out[i].Default
		}
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
}

// LoadFile reads one dashboard made from the UI: platforms/<platform>/dashboards/<id>.yaml.
func LoadFile(path, platform string) (Dashboard, error) {
	id := strings.TrimSuffix(strings.TrimSuffix(filepath.Base(path), ".yaml"), ".yml")
	if err := ValidID(id); err != nil {
		return Dashboard{}, fmt.Errorf("dashboard: %s: %w", path, err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return Dashboard{}, fmt.Errorf("dashboard: read %s: %w", path, err)
	}
	var d Dashboard
	if err := yaml.Unmarshal(b, &d); err != nil {
		return Dashboard{}, fmt.Errorf("dashboard: parse %s: %w", path, err)
	}
	d.ID = id
	d.Platform = platform
	d.Normalise()
	if d.Name == "" {
		d.Name = id
	}
	if err := d.Validate(); err != nil {
		return Dashboard{}, fmt.Errorf("dashboard: %s: %w", path, err)
	}
	return d, nil
}

// LoadPlatform reads a platform's shipped dashboards.yaml and every file made
// from the UI under dashboards/. An id in both is an error rather than a
// silent shadow.
func LoadPlatform(dir, platform string) ([]Dashboard, error) {
	var out []Dashboard
	for _, name := range []string{"dashboards.yaml", "dashboards.yml"} {
		shipped, err := Load(filepath.Join(dir, platform, name))
		if err != nil {
			return nil, err
		}
		if len(shipped) > 0 {
			out = append(out, shipped...)
			break
		}
	}
	entries, err := os.ReadDir(filepath.Join(dir, platform, SharedDir))
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("dashboard: read %s: %w", filepath.Join(dir, platform, SharedDir), err)
	}
	seen := make(map[string]bool, len(out))
	for _, d := range out {
		seen[d.ID] = true
	}
	for _, e := range entries {
		ext := filepath.Ext(e.Name())
		if e.IsDir() || (ext != ".yaml" && ext != ".yml") {
			continue
		}
		d, err := LoadFile(filepath.Join(dir, platform, SharedDir, e.Name()), platform)
		if err != nil {
			return nil, err
		}
		if seen[d.ID] {
			return nil, fmt.Errorf("dashboard: %s is defined both in dashboards.yaml and in %s/", d.ID, SharedDir)
		}
		seen[d.ID] = true
		out = append(out, d)
	}
	sortDashboards(out)
	return out, nil
}

// Save writes a dashboard made from the UI, whole and renamed into place.
func Save(dir string, d Dashboard) error {
	if err := ValidID(d.ID); err != nil {
		return err
	}
	d.Normalise()
	if err := d.Validate(); err != nil {
		return err
	}
	b, err := yaml.Marshal(d)
	if err != nil {
		return fmt.Errorf("dashboard: encode: %w", err)
	}
	folder := filepath.Join(dir, d.Platform, SharedDir)
	if err := os.MkdirAll(folder, 0o750); err != nil {
		return err
	}
	path := filepath.Join(folder, d.ID+".yaml")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Delete removes a dashboard made from the UI.
func Delete(dir, platform, id string) error {
	if err := ValidID(id); err != nil {
		return err
	}
	err := os.Remove(filepath.Join(dir, platform, SharedDir, id+".yaml"))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// Validate reports an arrangement the browser could not draw.
func (d Dashboard) Validate() error {
	if len(d.Rows) == 0 && len(d.Columns) > 0 {
		d.Rows = []Row{{Columns: d.Columns}}
	}
	if len(d.Rows) == 0 {
		return fmt.Errorf("dashboard %q has no rows", d.ID)
	}
	for ri, r := range d.Rows {
		if err := r.validate(d.ID, ri); err != nil {
			return err
		}
	}
	return nil
}

func (r Row) validate(id string, ri int) error {
	if len(r.Columns) == 0 {
		return fmt.Errorf("dashboard %q: row %d has no columns", id, ri+1)
	}
	for ci, c := range r.Columns {
		if len(c.Panels) == 0 {
			return fmt.Errorf("dashboard %q: row %d, column %d has no panels", id, ri+1, ci+1)
		}
		if c.Width < 0 {
			return fmt.Errorf("dashboard %q: row %d, column %d has a negative width", id, ri+1, ci+1)
		}
		for pi, p := range c.Panels {
			where := fmt.Sprintf("dashboard %q: row %d, column %d, panel %d", id, ri+1, ci+1, pi+1)
			if !p.Type.Valid() {
				return fmt.Errorf("%s: unknown type %q", where, p.Type)
			}
			if (p.Type == PanelChart || p.Type == PanelGauge) && p.Signal == "" {
				return fmt.Errorf("%s: a %s panel needs a signal", where, p.Type)
			}
			if p.Type == PanelChart && p.Sub != "" && p.Group == "" {
				return fmt.Errorf("%s: a sub-grouping needs a grouping", where)
			}
			if p.Sort != "" && !signal.ValidGaugeSort(p.Sort) {
				return fmt.Errorf("%s: sort %q is not one of %s", where, p.Sort, strings.Join(signal.GaugeSorts, ", "))
			}
			for _, level := range p.Levels {
				switch strings.ToUpper(level) {
				case "ERROR", "WARN", "INFO", "DEBUG", "TRACE":
				default:
					return fmt.Errorf("%s: %q is not an alert level", where, level)
				}
			}
		}
	}
	return nil
}

// Store holds the dashboards of every platform, replaced wholesale when a file
// changes.
type Store struct {
	mu         sync.RWMutex
	byPlatform map[string][]Dashboard
}

// NewStore builds an empty store.
func NewStore() *Store {
	return &Store{byPlatform: map[string][]Dashboard{}}
}

// Replace installs one platform's dashboards.
func (s *Store) Replace(platform string, dashboards []Dashboard) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(dashboards) == 0 {
		delete(s.byPlatform, platform)
		return
	}
	s.byPlatform[platform] = dashboards
}

// For returns a platform's dashboards.
func (s *Store) For(platform string) []Dashboard {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Dashboard, len(s.byPlatform[platform]))
	copy(out, s.byPlatform[platform])
	return out
}

// LoadAll reads every platforms/<platform>/dashboards.yaml under dir.
func LoadAll(dir string) (map[string][]Dashboard, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("dashboard: read %s: %w", dir, err)
	}
	out := map[string][]Dashboard{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dashboards, err := LoadPlatform(dir, e.Name())
		if err != nil {
			return nil, err
		}
		if len(dashboards) > 0 {
			out[e.Name()] = dashboards
		}
	}
	return out, nil
}
