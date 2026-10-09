package signal

import (
	"errors"
	"fmt"
	"sort"

	"gopkg.in/yaml.v3"
)

// Export writes signals in the signals.yaml format, without who made them, so
// the file can be imported on any platform. Built-in and auto-registered
// signals are left out: every platform has the first, and the second is not
// a definition anyone made.
func Export(platform string, defs []*Definition) ([]byte, error) {
	out := yamlCatalogue{Platform: platform, Signals: map[string]yamlDefinition{}}
	for _, d := range defs {
		if d.BuiltIn || d.AutoRegistered {
			continue
		}
		rd := toYAML(*d)
		rd.CreatedBy, rd.UpdatedBy = "", ""
		out.Signals[d.Name] = rd
	}
	return yaml.Marshal(out)
}

// ParseExport reads an exported file, or any platform's signals.yaml, as
// definitions for platform, sorted by name. One signal that could not be
// saved refuses the whole file, so an import never stops halfway.
func ParseExport(b []byte, platform string) ([]*Definition, error) {
	var raw yamlCatalogue
	if err := yaml.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("not a signals file: %w", err)
	}
	if len(raw.Signals) == 0 {
		return nil, errors.New("the file holds no signals")
	}
	names := make([]string, 0, len(raw.Signals))
	for name := range raw.Signals {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]*Definition, 0, len(names))
	for _, name := range names {
		if err := ValidName(name); err != nil {
			return nil, err
		}
		d, err := raw.Signals[name].toDefinition(platform, name)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		switch d.Kind {
		case KindTimeseries, KindGauge, KindLog, KindInfo:
		default:
			return nil, fmt.Errorf("%s: kind %q needs no definition", name, d.Kind)
		}
		d.applyDefaults()
		if err := d.Validate(); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		out = append(out, d)
	}
	return out, nil
}
