package signal

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Catalogue is one platform's signals, loaded from
// platforms/<platform>/signals.yaml. The file is data: adding a chart means
// editing it, not writing code.
type Catalogue struct {
	Platform string
	Signals  map[string]*Definition
	// Source is the file it came from, empty for a catalogue built in code.
	Source string
}

// yamlCatalogue mirrors the file format. It is separate from Definition so
// the on-disk shape can stay friendly (snake_case, omitted sections) without
// leaking into the rest of the server.
type yamlCatalogue struct {
	Platform string                    `yaml:"platform"`
	Signals  map[string]yamlDefinition `yaml:"signals"`
}

type yamlDefinition struct {
	Kind       string        `yaml:"kind"`
	Input      yamlInput     `yaml:"input,omitempty"`
	Retention  yamlRetention `yaml:"retention,omitempty"`
	MaxKeys    int           `yaml:"max_keys,omitempty"`
	Display    yamlDisplay   `yaml:"display,omitempty"`
	KeepPairs  [][2]string   `yaml:"keep_pairs,omitempty,flow"`
	Views      []yamlView    `yaml:"views,omitempty"`
	TTLSeconds int           `yaml:"ttl_seconds,omitempty"`
	PacketType string        `yaml:"packet_type,omitempty"`
	Merge      bool          `yaml:"merge,omitempty"`
	NoStatus   bool          `yaml:"no_status,omitempty"`
	CreatedBy  string        `yaml:"created_by,omitempty"`
	UpdatedBy  string        `yaml:"updated_by,omitempty"`
}

type yamlInput struct {
	Dims   []string          `yaml:"dims,omitempty"`
	Values map[string]string `yaml:"values,omitempty"`
}

type yamlRetention struct {
	HotDetailMinutes int  `yaml:"hot_detail_minutes,omitempty"`
	HotTotalsMinutes int  `yaml:"hot_totals_minutes,omitempty"`
	DurableDays      int  `yaml:"durable_days,omitempty"`
	DetailDays       int  `yaml:"detail_days,omitempty"`
	LogDays          int  `yaml:"log_days,omitempty"`
	ArchiveDays      *int `yaml:"archive_days,omitempty"`
	Versions         int  `yaml:"versions,omitempty"`
}

type yamlDisplay struct {
	Name   string            `yaml:"name,omitempty"`
	Dims   map[string]string `yaml:"dims,omitempty"`
	Labels map[string]string `yaml:"labels,omitempty"`
	Colors map[string]string `yaml:"colors,omitempty"`
}

type yamlView struct {
	ID        string            `yaml:"id"`
	Type      string            `yaml:"type"`
	Series    string            `yaml:"series,omitempty"`
	Value     string            `yaml:"value,omitempty"`
	Style     string            `yaml:"style,omitempty"`
	Top       int               `yaml:"top,omitempty"`
	Order     []string          `yaml:"order,omitempty"`
	Colors    map[string]string `yaml:"colors,omitempty"`
	Title     string            `yaml:"title,omitempty"`
	DefaultOn bool              `yaml:"default_on,omitempty"`
	Options   map[string]any    `yaml:"options,omitempty"`
}

// LoadCatalogue reads one catalogue file. The platform name comes from the
// file when it sets one, otherwise from the directory holding it.
func LoadCatalogue(path string) (*Catalogue, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("catalogue: read %s: %w", path, err)
	}
	var raw yamlCatalogue
	if err := yaml.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("catalogue: parse %s: %w", path, err)
	}
	platform := raw.Platform
	if platform == "" {
		platform = filepath.Base(filepath.Dir(path))
	}
	if platform == "" || platform == "." || platform == string(filepath.Separator) {
		return nil, fmt.Errorf("catalogue: %s: cannot tell which platform this is", path)
	}

	c := &Catalogue{Platform: platform, Signals: make(map[string]*Definition, len(raw.Signals)), Source: path}
	for name, rd := range raw.Signals {
		d, err := rd.toDefinition(platform, name)
		if err != nil {
			return nil, fmt.Errorf("catalogue: %s: %w", path, err)
		}
		d.ReadOnly = true
		c.Signals[name] = d
	}
	return c, nil
}

// LoadCatalogues reads every platform under dir: its shipped signals.yaml and
// the signals made in the designer. A missing directory is not an error: a
// fresh install simply has no platform described yet.
func LoadCatalogues(dir string) ([]*Catalogue, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("catalogue: read %s: %w", dir, err)
	}
	var out []*Catalogue
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		c, err := LoadPlatform(dir, e.Name())
		if err != nil {
			return nil, err
		}
		if len(c.Signals) > 0 {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Platform < out[j].Platform })
	return out, nil
}

func (rd yamlDefinition) toDefinition(platform, name string) (*Definition, error) {
	d := &Definition{
		Platform: platform,
		Name:     name,
		Kind:     Kind(strings.TrimSpace(rd.Kind)),
		Dims:     rd.Input.Dims,
		Values:   Values{Count: rd.Input.Values["count"], MS: rd.Input.Values["ms"]},
		Retention: Retention{
			HotDetailMinutes: rd.Retention.HotDetailMinutes,
			HotTotalsMinutes: rd.Retention.HotTotalsMinutes,
			DurableDays:      rd.Retention.DurableDays,
			DetailDays:       rd.Retention.DetailDays,
			LogDays:          rd.Retention.LogDays,
			ArchiveDays:      rd.Retention.ArchiveDays,
			Versions:         rd.Retention.Versions,
		},
		MaxKeys:    rd.MaxKeys,
		KeepPairs:  rd.KeepPairs,
		Display:    Display{Name: rd.Display.Name, Dims: rd.Display.Dims, Labels: rd.Display.Labels, Colors: rd.Display.Colors},
		TTLSeconds: rd.TTLSeconds,
		PacketType: strings.TrimSpace(rd.PacketType),
		Merge:      rd.Merge,
		NoStatus:   rd.NoStatus,
		CreatedBy:  rd.CreatedBy,
		UpdatedBy:  rd.UpdatedBy,
	}
	for _, v := range rd.Views {
		// The two shapes are deliberately separate types (one owns the yaml
		// spelling, one the JSON the browser sees) but identical field by
		// field, so the conversion is safe — and stops compiling the moment
		// they diverge, which is when the mapping has to be written out.
		d.Views = append(d.Views, View(v))
	}
	d.applyDefaults()
	if err := d.Validate(); err != nil {
		return nil, err
	}
	return d, nil
}
