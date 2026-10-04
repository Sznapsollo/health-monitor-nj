package legacy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/Sznapsollo/health-monitor-nj/internal/watchfs"
)

// MappingFile is the per-platform file translating a sender's own packet
// names into the adapter's native ones.
const MappingFile = "mapping.yaml"

// Native packet types the adapter understands.
const (
	TypeRequest          = "request"
	TypeJobReport        = "jobReport"
	TypeCustomWarning    = "customWarning"
	TypeCustomLog        = "customLog"
	TypeServerStatus     = "configServerListStatus"
	TypeJobsServerStatus = "configServerJobsListStatus"
	TypeMailServerStatus = "mailServerStatus"
	TypeQueuesLoad       = "jobQueuesLoad"
	TypeVPNUsersLoad     = "vpnUsersLoad"
)

var nativeTypes = map[string]bool{
	TypeRequest: true, TypeJobReport: true, TypeCustomWarning: true, TypeCustomLog: true,
	TypeServerStatus: true, TypeJobsServerStatus: true, TypeMailServerStatus: true,
	TypeQueuesLoad: true, TypeVPNUsersLoad: true,
}

// Mapping renames what a sender calls things before the adapter reads a
// packet: `types` maps values of the type field, `fields` maps field names,
// and `origins` maps the origin field's value to the word shown in an
// alert's category ("default" for any other origin).
type Mapping struct {
	Types   map[string]string `yaml:"types"`
	Fields  map[string]string `yaml:"fields"`
	Origins map[string]string `yaml:"origins"`
}

// LoadMapping reads a mapping file. A missing file is no mapping: packets are
// read with their native names.
func LoadMapping(path string) (*Mapping, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var m Mapping
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := m.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &m, nil
}

func (m *Mapping) validate() error {
	var errs []error
	for _, from := range sortedKeys(m.Types) {
		if to := m.Types[from]; !nativeTypes[to] {
			errs = append(errs, fmt.Errorf("types.%s: %q is not a native type (%s)", from, to, strings.Join(sortedKeys(nativeTypes), ", ")))
		}
	}
	for _, from := range sortedKeys(m.Fields) {
		if to := m.Fields[from]; !known[to] {
			errs = append(errs, fmt.Errorf("fields.%s: %q is not a native field", from, to))
		}
	}
	return errors.Join(errs...)
}

// rename rewrites the object keys named in Fields in one pass over the
// datagram. Strings are skipped whole, so a value can never be mistaken for a
// key; nothing is copied unless a key is renamed.
func (m *Mapping) rename(raw []byte) []byte {
	if m == nil || len(m.Fields) == 0 {
		return raw
	}
	var out []byte
	copied := 0
	for i := 0; i < len(raw); i++ {
		if raw[i] != '"' {
			continue
		}
		end := i + 1
		for end < len(raw) && raw[end] != '"' {
			if raw[end] == '\\' {
				end++
			}
			end++
		}
		if end >= len(raw) {
			break
		}
		next := end + 1
		for next < len(raw) && (raw[next] == ' ' || raw[next] == '\t' || raw[next] == '\n' || raw[next] == '\r') {
			next++
		}
		if next < len(raw) && raw[next] == ':' {
			if to, ok := m.Fields[string(raw[i+1:end])]; ok {
				out = append(out, raw[copied:i+1]...)
				out = append(out, to...)
				copied = end
			}
		}
		i = end
	}
	if out == nil {
		return raw
	}
	return append(out, raw[copied:]...)
}

func (m *Mapping) packetType(t string) string {
	if m != nil {
		if native, ok := m.Types[t]; ok {
			return native
		}
	}
	return t
}

func (m *Mapping) originWord(origin string) string {
	origin = strings.ToLower(strings.TrimSpace(origin))
	if m != nil {
		if w, ok := m.Origins[origin]; ok {
			return w
		}
		if w, ok := m.Origins["default"]; ok {
			return w
		}
	}
	if origin == "" {
		return "APP"
	}
	return strings.ToUpper(origin)
}

// WatchMapping reloads the platform's mapping file when it changes. A broken
// file keeps the previous mapping in force.
func WatchMapping(ctx context.Context, dir, platform string, a *Adapter, log *slog.Logger) error {
	want := filepath.Join(dir, platform, MappingFile)
	return watchfs.Run(ctx, watchfs.Options{
		Root:        dir,
		Log:         log,
		Interesting: func(path string) bool { return filepath.Clean(path) == filepath.Clean(want) },
		OnChange: func(path string) {
			m, err := LoadMapping(path)
			if err != nil {
				log.Error("mapping not reloaded", "file", path, "error", err)
				return
			}
			a.SetMapping(m)
			log.Info("mapping reloaded", "platform", platform, "file", path)
		},
	})
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
