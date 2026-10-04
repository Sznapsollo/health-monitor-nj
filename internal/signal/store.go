package signal

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// DesignedDir holds the signals made in the designer, one file each.
const DesignedDir = "signals"

var namePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,63}$`)

// ValidName reports a signal or platform name that cannot be a file name.
func ValidName(name string) error {
	if !namePattern.MatchString(name) {
		return fmt.Errorf("name %q: start with a letter, then letters, digits, '.', '-' or '_' (64 at most)", name)
	}
	return nil
}

// LoadPlatform reads a platform's shipped signals.yaml and every file under signals/.
func LoadPlatform(dir, platform string) (*Catalogue, error) {
	c := &Catalogue{Platform: platform, Signals: map[string]*Definition{}}
	for _, name := range []string{"signals.yaml", "signals.yml"} {
		path := filepath.Join(dir, platform, name)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		shipped, err := LoadCatalogue(path)
		if err != nil {
			return nil, err
		}
		c = shipped
		break
	}

	folder := filepath.Join(dir, platform, DesignedDir)
	entries, err := os.ReadDir(folder)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("catalogue: read %s: %w", folder, err)
	}
	for _, e := range entries {
		ext := filepath.Ext(e.Name())
		if e.IsDir() || (ext != ".yaml" && ext != ".yml") {
			continue
		}
		d, err := loadDesigned(filepath.Join(folder, e.Name()), c.Platform)
		if err != nil {
			return nil, err
		}
		if _, dup := c.Signals[d.Name]; dup {
			return nil, fmt.Errorf("catalogue: %s is defined both in signals.yaml and in %s/", d.Name, DesignedDir)
		}
		c.Signals[d.Name] = d
	}
	return c, nil
}

func loadDesigned(path, platform string) (*Definition, error) {
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if err := ValidName(name); err != nil {
		return nil, fmt.Errorf("catalogue: %s: %w", path, err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("catalogue: read %s: %w", path, err)
	}
	var rd yamlDefinition
	if err := yaml.Unmarshal(b, &rd); err != nil {
		return nil, fmt.Errorf("catalogue: parse %s: %w", path, err)
	}
	d, err := rd.toDefinition(platform, name)
	if err != nil {
		return nil, fmt.Errorf("catalogue: %s: %w", path, err)
	}
	return d, nil
}

// Save writes a signal made in the designer, whole and renamed into place.
func Save(dir string, d Definition) error {
	if err := ValidName(d.Platform); err != nil {
		return err
	}
	if err := ValidName(d.Name); err != nil {
		return err
	}
	d.applyDefaults()
	if err := d.Validate(); err != nil {
		return err
	}
	b, err := yaml.Marshal(toYAML(d))
	if err != nil {
		return fmt.Errorf("catalogue: encode %s: %w", d.Name, err)
	}
	folder := filepath.Join(dir, d.Platform, DesignedDir)
	if err := os.MkdirAll(folder, 0o750); err != nil {
		return err
	}
	path := filepath.Join(folder, d.Name+".yaml")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Delete removes a signal made in the designer.
func Delete(dir, platform, name string) error {
	if err := ValidName(platform); err != nil {
		return err
	}
	if err := ValidName(name); err != nil {
		return err
	}
	err := os.Remove(filepath.Join(dir, platform, DesignedDir, name+".yaml"))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func toYAML(d Definition) yamlDefinition {
	rd := yamlDefinition{
		Kind:  string(d.Kind),
		Input: yamlInput{Dims: d.Dims},
		Retention: yamlRetention{
			HotDetailMinutes: d.Retention.HotDetailMinutes,
			HotTotalsMinutes: d.Retention.HotTotalsMinutes,
			DurableDays:      d.Retention.DurableDays,
			DetailDays:       d.Retention.DetailDays,
		},
		MaxKeys:    d.MaxKeys,
		KeepPairs:  d.KeepPairs,
		Display:    yamlDisplay{Name: d.Display.Name, Dims: d.Display.Dims, Labels: d.Display.Labels, Colors: d.Display.Colors},
		TTLSeconds: d.TTLSeconds,
		PacketType: d.PacketType,
		CreatedBy:  d.CreatedBy,
		UpdatedBy:  d.UpdatedBy,
	}
	if d.Values.Count != "" || d.Values.MS != "" {
		rd.Input.Values = map[string]string{}
		if d.Values.Count != "" {
			rd.Input.Values["count"] = d.Values.Count
		}
		if d.Values.MS != "" {
			rd.Input.Values["ms"] = d.Values.MS
		}
	}
	for _, v := range d.Views {
		rd.Views = append(rd.Views, yamlView(v))
	}
	if d.Kind == KindInfo {
		return yamlDefinition{
			Kind:       rd.Kind,
			PacketType: d.PacketType,
			Merge:      d.Merge,
			NoStatus:   d.NoStatus,
			Retention:  yamlRetention{Versions: d.Retention.Versions, DurableDays: d.Retention.DurableDays},
			Display:    yamlDisplay{Name: d.Display.Name},
			CreatedBy:  d.CreatedBy,
			UpdatedBy:  d.UpdatedBy,
		}
	}
	if d.Kind == KindLog {
		// A log has no charts: only what the retention of its days needs.
		return yamlDefinition{
			Kind:      rd.Kind,
			Retention: yamlRetention{LogDays: d.Retention.LogDays, ArchiveDays: d.Retention.ArchiveDays},
			Display:   yamlDisplay{Name: d.Display.Name},
			CreatedBy: d.CreatedBy,
			UpdatedBy: d.UpdatedBy,
		}
	}
	return rd
}
