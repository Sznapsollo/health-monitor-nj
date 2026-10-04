// Package signal holds what the server knows about each stream of data: its
// kind, its dimensions, how long to keep it and how the UI should draw it.
// A platform is normally described entirely by a catalogue file, so adding a
// chart is a configuration change, not a code change.
package signal

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Kind is the generic behaviour a signal follows. One implementation per kind
// serves every signal of that kind, for every platform.
type Kind string

const (
	KindTimeseries Kind = "timeseries"
	KindGauge      Kind = "gauge"
	KindStatus     Kind = "status"
	KindAlert      Kind = "alert"
	KindLog        Kind = "log"
	// KindInfo keeps whole packets of one type as they arrived, the latest
	// few per sender, to be read rather than charted.
	KindInfo Kind = "info"
)

// Valid reports whether k is a kind the server implements.
func (k Kind) Valid() bool {
	switch k {
	case KindTimeseries, KindGauge, KindStatus, KindAlert, KindLog, KindInfo:
		return true
	}
	return false
}

// Definition is one signal of one platform.
type Definition struct {
	Platform  string
	Name      string
	Kind      Kind
	Dims      []string
	Values    Values
	Retention Retention
	MaxKeys   int
	Display   Display
	Views     []View
	// KeepPairs are the dimension pairs stored always, not only while a chart
	// shows them, so their history exists when one is opened later.
	KeepPairs [][2]string
	// Gauges only; zero means the store's default.
	TTLSeconds int
	// PacketType is the sender's packet type an info signal keeps, or a
	// timeseries charts from its top-level fields.
	PacketType string
	// Merge folds every report of a sender into one, instead of keeping
	// Versions of them.
	Merge bool
	// NoStatus keeps an info signal's senders off the Status tab, so a sender
	// reporting rarely is never taken for one gone offline.
	NoStatus bool

	// AutoRegistered marks a signal the server invented on the fly because a
	// packet arrived for a name the catalogue does not declare.
	AutoRegistered bool
	// BuiltIn marks a signal the monitor feeds itself, on every platform.
	BuiltIn bool
	// ReadOnly marks a signal from the shipped signals.yaml.
	ReadOnly  bool
	CreatedBy string
	UpdatedBy string
}

// Values says which fields of an incoming metric carry the count and the
// duration, so a sender need not rename anything to feed a signal.
type Values struct {
	// Count is the field holding how many observations the packet stands for;
	// empty means the packet counts as one.
	Count string
	// MS is the field holding a duration in milliseconds; empty means the
	// signal has no duration and only counts.
	MS string
}

// Retention is how much of a signal is kept, and where.
type Retention struct {
	// HotDetailMinutes keeps every dimension of every minute in memory.
	HotDetailMinutes int
	// HotTotalsMinutes keeps only the minute totals, which cost almost
	// nothing and serve a day or two of the main chart instantly.
	HotTotalsMinutes int
	// DurableDays is how long anything of the signal stays in SQLite.
	DurableDays int
	// LogDays is how long a log signal's days stay in the database; 0 leaves
	// it to the configuration.
	LogDays int
	// ArchiveDays is how long a log signal's archive files are kept after
	// they are written; nil leaves it to the configuration, 0 drops old days.
	ArchiveDays *int
	// DetailDays is how long the per-value rows stay per minute; after that
	// they are summed per hour.
	DetailDays int
	// Versions is how many packets an info signal keeps per sender.
	Versions int
}

// Display carries the human-readable names, which belong to the platform
// rather than to a locale file.
type Display struct {
	Name string
	Dims map[string]string
	// Labels maps a dimension to the one that names its values, so an
	// account id can be shown by its accountName.
	Labels map[string]string
	// Colors maps a dimension value to the colour its series is drawn in,
	// whichever dimension the chart is grouped by.
	Colors map[string]string
}

// DimName is the display name of a dimension, falling back to its identifier.
func (d Display) DimName(dim string) string {
	if n, ok := d.Dims[dim]; ok && n != "" {
		return n
	}
	return dim
}

// View is one way of drawing a signal. The server validates it against the
// definition and passes it to the browser, which owns the renderers.
type View struct {
	ID        string            `json:"id"`
	Type      string            `json:"type"`
	Series    string            `json:"series,omitempty"`
	Value     string            `json:"value,omitempty"`
	Style     string            `json:"style,omitempty"`
	Top       int               `json:"top,omitempty"`
	Order     []string          `json:"order,omitempty"`
	Colors    map[string]string `json:"colors,omitempty"`
	Title     string            `json:"title,omitempty"`
	DefaultOn bool              `json:"defaultOn,omitempty"`
	// Options carries renderer-specific keys the server does not interpret.
	Options map[string]any `json:"options,omitempty"`
}

// Defaults used when the catalogue leaves a field out:
// two hours of full detail and forty-eight hours of totals.
const (
	DefaultHotDetailMinutes = 120
	DefaultHotTotalsMinutes = 2880
	DefaultDurableDays      = 30
	DefaultDetailDays       = 3
	DefaultMaxKeys          = 10000
	DefaultInfoVersions     = 20
)

// applyDefaults fills in what the catalogue omitted.
func (d *Definition) applyDefaults() {
	if d.Retention.HotDetailMinutes == 0 {
		d.Retention.HotDetailMinutes = DefaultHotDetailMinutes
	}
	if d.Retention.HotTotalsMinutes == 0 {
		d.Retention.HotTotalsMinutes = DefaultHotTotalsMinutes
	}
	if d.Retention.DurableDays == 0 {
		d.Retention.DurableDays = DefaultDurableDays
	}
	if d.Retention.DetailDays == 0 {
		d.Retention.DetailDays = min(DefaultDetailDays, d.Retention.DurableDays)
	}
	if d.MaxKeys == 0 {
		d.MaxKeys = DefaultMaxKeys
	}
	if d.Display.Name == "" {
		d.Display.Name = d.Name
	}
	if d.Kind == KindInfo && d.Retention.Versions == 0 {
		d.Retention.Versions = DefaultInfoVersions
	}
	if d.Kind == KindTimeseries && d.Values.Count == "" {
		d.Values.Count = "count"
	}
}

// Validate reports a definition the server cannot serve.
func (d Definition) Validate() error {
	if d.Name == "" {
		return fmt.Errorf("signal: definition without a name")
	}
	if !d.Kind.Valid() {
		return fmt.Errorf("signal %q: unknown kind %q", d.Name, d.Kind)
	}
	seen := make(map[string]bool, len(d.Dims))
	for _, dim := range d.Dims {
		if dim == "" {
			return fmt.Errorf("signal %q: empty dimension name", d.Name)
		}
		if seen[dim] {
			return fmt.Errorf("signal %q: dimension %q declared twice", d.Name, dim)
		}
		seen[dim] = true
	}
	if d.Retention.LogDays < 0 || (d.Retention.ArchiveDays != nil && *d.Retention.ArchiveDays < 0) {
		return fmt.Errorf("signal %q: log_days and archive_days cannot be negative", d.Name)
	}
	if d.Kind == KindInfo && strings.TrimSpace(d.PacketType) == "" {
		return fmt.Errorf("signal %q: an info signal needs packet_type", d.Name)
	}
	if d.Retention.Versions < 0 {
		return fmt.Errorf("signal %q: versions cannot be negative", d.Name)
	}
	if d.TTLSeconds < 0 {
		return fmt.Errorf("signal %q: ttl_seconds is negative", d.Name)
	}
	if d.Retention.HotTotalsMinutes < d.Retention.HotDetailMinutes {
		return fmt.Errorf("signal %q: hot_totals_minutes (%d) is below hot_detail_minutes (%d)",
			d.Name, d.Retention.HotTotalsMinutes, d.Retention.HotDetailMinutes)
	}
	if d.Retention.DetailDays > d.Retention.DurableDays {
		return fmt.Errorf("signal %q: detail_days (%d) is above durable_days (%d)",
			d.Name, d.Retention.DetailDays, d.Retention.DurableDays)
	}
	for _, p := range d.KeepPairs {
		if p[0] == p[1] || !d.hasDim(p[0]) || !d.hasDim(p[1]) {
			return fmt.Errorf("signal %q: keep_pairs %v must name two different declared dimensions", d.Name, p)
		}
	}
	for dim, by := range d.Display.Labels {
		if dim == by || !d.hasDim(dim) || !d.hasDim(by) {
			return fmt.Errorf("signal %q: display.labels %s: %s must name two different declared dimensions", d.Name, dim, by)
		}
	}
	for value, color := range d.Display.Colors {
		if value == "" || !hexColor.MatchString(color) {
			return fmt.Errorf("signal %q: display.colors %q: %q must be a colour like #2e7d32", d.Name, value, color)
		}
	}
	for _, v := range d.Views {
		if err := v.validate(d); err != nil {
			return err
		}
	}
	return nil
}

func (v View) validate(d Definition) error {
	if v.ID == "" {
		return fmt.Errorf("signal %q: view without an id", d.Name)
	}
	if v.Type == "" {
		return fmt.Errorf("signal %q: view %q without a type", d.Name, v.ID)
	}
	if v.Series != "" && !d.hasDim(v.Series) {
		return fmt.Errorf("signal %q: view %q draws a series per %q, which is not a declared dimension",
			d.Name, v.ID, v.Series)
	}
	if sort, ok := v.Options["sort"]; ok {
		if s, _ := sort.(string); !ValidGaugeSort(s) {
			return fmt.Errorf("signal %q: view %q: sort %v is not one of %s", d.Name, v.ID, sort, strings.Join(GaugeSorts, ", "))
		}
	}
	return nil
}

var hexColor = regexp.MustCompile(`^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)

// GaugeSorts are the orders a bar gauge can draw its bars in.
var GaugeSorts = []string{"label", "labelDesc", "value", "valueDesc"}

// ValidGaugeSort reports whether s is one of GaugeSorts.
func ValidGaugeSort(s string) bool {
	return slices.Contains(GaugeSorts, s)
}

func (d Definition) hasDim(dim string) bool {
	for _, have := range d.Dims {
		if have == dim {
			return true
		}
	}
	return false
}

// PacketsSignal counts what each sender sends, per packet type; the monitor
// feeds it from the intake.
const PacketsSignal = "packets"

// PacketsDefinition is the built-in packets signal of one platform: counts
// only, per sender and type, a day per minute and then per hour, for 60 days.
func PacketsDefinition(platform string) *Definition {
	d := &Definition{
		Platform: platform,
		Name:     PacketsSignal,
		Kind:     KindTimeseries,
		Dims:     []string{"sender", "type"},
		Values:   Values{Count: "count"},
		Retention: Retention{
			HotDetailMinutes: DefaultHotDetailMinutes,
			HotTotalsMinutes: DefaultHotTotalsMinutes,
			DurableDays:      60,
			DetailDays:       1,
		},
		Display: Display{
			Name: "Packets received",
			Dims: map[string]string{"sender": "Sender", "type": "Packet type"},
		},
		Views: []View{
			{ID: "all", Type: "minuteSeries", Value: "count", Style: "column", DefaultOn: true},
			{ID: "byGroup", Type: "minuteSeriesPerGroup", Value: "count", Style: "column", Top: 20, DefaultOn: true},
		},
		BuiltIn:  true,
		ReadOnly: true,
	}
	d.applyDefaults()
	return d
}
