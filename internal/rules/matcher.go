package rules

import (
	"path"
	"strings"

	"github.com/Sznapsollo/health-monitor-nj/internal/protocol"
	"github.com/Sznapsollo/health-monitor-nj/internal/state"
)

// matcher is the compiled form of a rule set: an exact map, a prefix list
// sorted longest first, and the globs in file order. It is rebuilt whenever
// the file changes, never per packet.
type matcher struct {
	exact  map[string][]Override
	prefix []Override
	glob   []Override
}

// Compile rebuilds the matcher. Call it after changing a set by hand — the
// UI builds sets field by field, and a stale matcher would quietly apply the
// rules that were there before.
func (s *Set) Compile() { s.compile() }

func (s *Set) compile() { s.matcher = s.build() }

func (s *Set) build() *matcher {
	m := &matcher{exact: map[string][]Override{}}
	for _, o := range s.Latency.Overrides {
		switch {
		case o.Path != "":
			m.exact[o.Path] = append(m.exact[o.Path], o)
		case o.Prefix != "":
			m.prefix = append(m.prefix, o)
		case o.Glob != "":
			m.glob = append(m.glob, o)
		}
	}
	m.prefix = sortOverrides(m.prefix)
	return m
}

// Subject is what a rule is asked about: one aggregated slice of traffic.
type Subject struct {
	Signal string
	Dims   map[string]string
	Agg    state.Agg
}

// Path is the url the rules match on, empty for signals that have none.
func (s Subject) Path() string { return s.Dims["url"] }

// Decision is what the rules say about a subject.
type Decision struct {
	// Alert is true when something should be raised.
	Alert bool
	Level protocol.Level
	// ThresholdMS is the limit that was applied, for the message.
	ThresholdMS float64
	// Matched describes which rule decided, for the UI and for silencing.
	Matched string
	// Ignored says a rule deliberately said nothing.
	Ignored bool
}

// EvaluateLatency decides whether an aggregate is slow enough to alert about.
// It is asked once per aggregated batch, not once per packet, so it looks at
// the slowest measurement in the slice.
func (s *Set) EvaluateLatency(subject Subject) Decision {
	if subject.Agg.Samples == 0 {
		return Decision{}
	}
	// A set nobody compiled gets a matcher for this call only: writing it
	// back would race with other goroutines reading the same set.
	m := s.matcher
	if m == nil {
		m = s.build()
	}

	o, matched, found := m.override(subject)
	if found && o.Ignore {
		return Decision{Ignored: true, Matched: matched}
	}

	threshold := s.Latency.DefaultMS
	level := s.Latency.Level
	rule := "latency/default"
	if found {
		threshold = o.MS
		rule = matched
		if o.Level != "" {
			level = o.Level
		}
	}
	if level == "" {
		level = protocol.LevelWarn
	}
	if threshold <= 0 || subject.Agg.MaxMS < threshold {
		return Decision{ThresholdMS: threshold, Matched: rule}
	}
	return Decision{Alert: true, Level: level, ThresholdMS: threshold, Matched: rule}
}

// override finds the most specific rule for a subject: exact path, then the
// longest matching prefix, then a glob in file order.
func (m *matcher) override(subject Subject) (Override, string, bool) {
	p := subject.Path()
	if p == "" {
		return Override{}, "", false
	}

	for _, o := range m.exact[p] {
		if matchesExtras(o, subject) {
			return o, "latency/path=" + o.Path, true
		}
	}
	for _, o := range m.prefix {
		if strings.HasPrefix(p, o.Prefix) && matchesExtras(o, subject) {
			return o, "latency/prefix=" + o.Prefix, true
		}
	}
	for _, o := range m.glob {
		if matchGlob(o.Glob, p) && matchesExtras(o, subject) {
			return o, "latency/glob=" + o.Glob, true
		}
	}
	return Override{}, "", false
}

// matchesExtras applies the optional account, user and server narrowing.
func matchesExtras(o Override, subject Subject) bool {
	if o.Account != "" && subject.Dims["account"] != o.Account {
		return false
	}
	if o.User != "" && subject.Dims["user"] != o.User {
		return false
	}
	if o.ServerName != "" && subject.Dims["serverName"] != o.ServerName {
		return false
	}
	return true
}

// matchGlob supports `*` within one path segment and `**` across segments,
// which is what the rules file's examples use.
func matchGlob(pattern, value string) bool {
	if !strings.Contains(pattern, "**") {
		ok, err := path.Match(pattern, value)
		return err == nil && ok
	}
	// `a/**/b` becomes a prefix and a suffix that both have to match, with
	// any number of segments between them.
	parts := strings.SplitN(pattern, "**", 2)
	prefix, suffix := parts[0], parts[1]
	if !strings.HasPrefix(value, prefix) {
		return false
	}
	rest := value[len(prefix):]
	if suffix == "" {
		return true
	}
	if !strings.Contains(suffix, "*") {
		return strings.HasSuffix(rest, suffix) || strings.Contains(rest, suffix)
	}
	for i := 0; i <= len(rest); i++ {
		if ok, err := path.Match(suffix, rest[i:]); err == nil && ok {
			return true
		}
	}
	return false
}

// Muted reports whether a message is one the platform has asked never to hear
// about, which replaces the old per-browser ignore lists.
func (s *Set) Muted(message string, level protocol.Level) bool {
	lower := strings.ToLower(message)
	for _, m := range s.Mute {
		if m.Contains == "" {
			continue
		}
		if m.Level != "" && m.Level != level {
			continue
		}
		if strings.Contains(lower, strings.ToLower(m.Contains)) {
			return true
		}
	}
	return false
}
