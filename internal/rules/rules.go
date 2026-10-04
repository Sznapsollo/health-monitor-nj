// Package rules turns the thresholds that used to be hard-coded in the old
// server into data: one file per platform, editable from the UI, hot-reloaded.
package rules

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/protocol"
	"gopkg.in/yaml.v3"
)

// Defaults used when a rules file leaves something out. The latency default is
// deliberately 1000 ms rather than the old server's 300 ms, which alerted on
// almost every request.
const (
	DefaultLatencyMS   = 1000
	DefaultOfflineSecs = 300
	// DefaultRepeatSecs is how often a server still down is alerted again.
	DefaultRepeatSecs  = 300
	DefaultGroupWindow = time.Minute
)

// Set is one platform's rules.
type Set struct {
	Platform string
	Latency  Latency
	Offline  Offline
	Groups   Groups
	Mute     []MutePattern
	// Source is the file it came from, empty for defaults built in code.
	Source string

	matcher *matcher
}

// Latency alerts when a request takes longer than it should.
type Latency struct {
	DefaultMS float64
	Level     protocol.Level
	Overrides []Override
}

// Override is one more specific threshold. Exactly one of Path, Prefix or Glob
// is set; the most specific match wins, in that order.
type Override struct {
	Path   string
	Prefix string
	Glob   string
	MS     float64
	Level  protocol.Level
	Ignore bool

	// Optional extra match keys, so a threshold can be narrowed to one
	// account, user or server.
	Account    string
	User       string
	ServerName string
}

// Offline alerts when something that sends heartbeats goes quiet.
type Offline struct {
	AfterSeconds int
	// RepeatSeconds reminds, on the same alert, while it stays quiet; 0
	// alerts once.
	RepeatSeconds int
}

// Groups says how long repeats sharing a group key collapse.
type Groups struct {
	WindowSeconds int
}

// MutePattern is the server-side replacement for the old per-browser
// "ignored messages" list.
type MutePattern struct {
	Contains string
	Level    protocol.Level
}

// Default returns a usable rule set for a platform with no file yet.
func Default(platform string) *Set {
	s := &Set{
		Platform: platform,
		Latency:  Latency{DefaultMS: DefaultLatencyMS, Level: protocol.LevelWarn},
		Offline:  Offline{AfterSeconds: DefaultOfflineSecs, RepeatSeconds: DefaultRepeatSecs},
		Groups:   Groups{WindowSeconds: int(DefaultGroupWindow.Seconds())},
	}
	s.compile()
	return s
}

// GroupWindow is how long repeats collapse.
func (s *Set) GroupWindow() time.Duration {
	if s.Groups.WindowSeconds <= 0 {
		return DefaultGroupWindow
	}
	return time.Duration(s.Groups.WindowSeconds) * time.Second
}

// RepeatEvery is how often a quiet entity is alerted again; 0 means once.
func (s *Set) RepeatEvery() time.Duration {
	if s.Offline.RepeatSeconds <= 0 {
		return 0
	}
	return time.Duration(s.Offline.RepeatSeconds) * time.Second
}

// OfflineAfter is how long an entity may stay silent.
func (s *Set) OfflineAfter() time.Duration {
	if s.Offline.AfterSeconds <= 0 {
		return DefaultOfflineSecs * time.Second
	}
	return time.Duration(s.Offline.AfterSeconds) * time.Second
}

// yamlSet mirrors the rules file format.
type yamlSet struct {
	Platform string `yaml:"platform"`
	Latency  struct {
		DefaultMS float64        `yaml:"default_ms"`
		Level     string         `yaml:"level,omitempty"`
		Overrides []yamlOverride `yaml:"overrides,omitempty"`
	} `yaml:"latency"`
	Offline struct {
		AfterSeconds  int  `yaml:"after_seconds"`
		RepeatSeconds *int `yaml:"repeat_seconds,omitempty"`
	} `yaml:"offline"`
	Groups struct {
		WindowSeconds int `yaml:"window_seconds"`
	} `yaml:"groups"`
	Mute []yamlMute `yaml:"mute,omitempty"`
}

// yamlOverride is one row of the overrides table. Empty fields are left out of
// a saved file, so what is written stays as readable as what a person types.
type yamlOverride struct {
	Path       string  `yaml:"path,omitempty"`
	Prefix     string  `yaml:"prefix,omitempty"`
	Glob       string  `yaml:"glob,omitempty"`
	MS         float64 `yaml:"ms,omitempty"`
	Level      string  `yaml:"level,omitempty"`
	Ignore     bool    `yaml:"ignore,omitempty"`
	Account    string  `yaml:"account,omitempty"`
	User       string  `yaml:"user,omitempty"`
	ServerName string  `yaml:"serverName,omitempty"`
}

type yamlMute struct {
	Contains string `yaml:"contains"`
	Level    string `yaml:"level,omitempty"`
}

// Load reads one platform's rules. A missing file is not an error: the
// defaults apply until someone saves rules from the UI.
func Load(path string) (*Set, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Default(platformOf(path)), nil
	}
	if err != nil {
		return nil, fmt.Errorf("rules: read %s: %w", path, err)
	}

	var raw yamlSet
	if err := yaml.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("rules: parse %s: %w", path, err)
	}

	platform := raw.Platform
	if platform == "" {
		platform = platformOf(path)
	}
	s := &Set{
		Platform: platform,
		Source:   path,
		Latency: Latency{
			DefaultMS: raw.Latency.DefaultMS,
			Level:     level(raw.Latency.Level, protocol.LevelWarn),
		},
		Offline: Offline{AfterSeconds: raw.Offline.AfterSeconds, RepeatSeconds: DefaultRepeatSecs},
		Groups:  Groups{WindowSeconds: raw.Groups.WindowSeconds},
	}
	if raw.Offline.RepeatSeconds != nil {
		s.Offline.RepeatSeconds = max(0, *raw.Offline.RepeatSeconds)
	}
	if s.Latency.DefaultMS <= 0 {
		s.Latency.DefaultMS = DefaultLatencyMS
	}
	for _, o := range raw.Latency.Overrides {
		s.Latency.Overrides = append(s.Latency.Overrides, Override{
			Path: o.Path, Prefix: o.Prefix, Glob: o.Glob,
			MS: o.MS, Level: level(o.Level, ""), Ignore: o.Ignore,
			Account: o.Account, User: o.User, ServerName: o.ServerName,
		})
	}
	for _, m := range raw.Mute {
		if strings.TrimSpace(m.Contains) == "" {
			continue
		}
		s.Mute = append(s.Mute, MutePattern{Contains: m.Contains, Level: level(m.Level, "")})
	}

	if err := s.Validate(); err != nil {
		return nil, fmt.Errorf("rules: %s: %w", path, err)
	}
	s.compile()
	return s, nil
}

// Save writes the rules back, which is what the Settings panel does.
func Save(path string, s *Set) error {
	raw := yamlSet{Platform: s.Platform}
	raw.Latency.DefaultMS = s.Latency.DefaultMS
	raw.Latency.Level = string(s.Latency.Level)
	for _, o := range s.Latency.Overrides {
		raw.Latency.Overrides = append(raw.Latency.Overrides, yamlOverride{
			Path: o.Path, Prefix: o.Prefix, Glob: o.Glob, MS: o.MS,
			Level: string(o.Level), Ignore: o.Ignore,
			Account: o.Account, User: o.User, ServerName: o.ServerName,
		})
	}
	raw.Offline.AfterSeconds = s.Offline.AfterSeconds
	repeat := s.Offline.RepeatSeconds
	raw.Offline.RepeatSeconds = &repeat
	raw.Groups.WindowSeconds = s.Groups.WindowSeconds
	for _, m := range s.Mute {
		raw.Mute = append(raw.Mute, yamlMute{Contains: m.Contains, Level: string(m.Level)})
	}

	b, err := yaml.Marshal(raw)
	if err != nil {
		return fmt.Errorf("rules: encode: %w", err)
	}
	header := "# healthMonitorNJ alert rules. Written by the Settings panel and\n" +
		"# hot-reloaded; edits by hand are picked up the same way.\n"
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	// Written whole and renamed into place, so a half-written file is never
	// what the watcher picks up.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append([]byte(header), b...), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Validate reports rules the server cannot apply.
func (s *Set) Validate() error {
	if s.Latency.DefaultMS < 0 {
		return fmt.Errorf("latency.default_ms is negative")
	}
	for i, o := range s.Latency.Overrides {
		set := 0
		for _, v := range []string{o.Path, o.Prefix, o.Glob} {
			if v != "" {
				set++
			}
		}
		if set == 0 {
			return fmt.Errorf("override %d matches nothing: give it a path, prefix or glob", i+1)
		}
		if set > 1 {
			return fmt.Errorf("override %d sets more than one of path, prefix and glob", i+1)
		}
		if !o.Ignore && o.MS <= 0 {
			return fmt.Errorf("override %d has no threshold and is not an ignore", i+1)
		}
		if o.Glob != "" {
			if _, err := filepath.Match(strings.ReplaceAll(o.Glob, "**", "*"), "x"); err != nil {
				return fmt.Errorf("override %d has an unusable glob %q: %w", i+1, o.Glob, err)
			}
		}
	}
	return nil
}

func ParseLevel(s string, fallback protocol.Level) protocol.Level {
	return level(s, fallback)
}

func level(s string, fallback protocol.Level) protocol.Level {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "ERROR":
		return protocol.LevelError
	case "WARN", "WARNING":
		return protocol.LevelWarn
	case "INFO":
		return protocol.LevelInfo
	case "DEBUG":
		return protocol.LevelDebug
	case "TRACE":
		return protocol.LevelTrace
	}
	return fallback
}

func platformOf(path string) string {
	return filepath.Base(filepath.Dir(path))
}

// sortOverrides keeps the longest prefixes first, so the most specific prefix
// wins without the file having to be in any particular order.
func sortOverrides(in []Override) []Override {
	out := append([]Override(nil), in...)
	sort.SliceStable(out, func(i, j int) bool {
		return len(out[i].Prefix) > len(out[j].Prefix)
	})
	return out
}
