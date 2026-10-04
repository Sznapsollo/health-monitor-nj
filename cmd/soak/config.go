package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type config struct {
	Platform   string          `yaml:"platform"`
	Protocol   string          `yaml:"protocol"`
	Duration   time.Duration   `yaml:"duration"`
	Servers    serversConfig   `yaml:"servers"`
	Requests   requestsConfig  `yaml:"requests"`
	Alerts     alertsConfig    `yaml:"alerts"`
	Queues     queuesConfig    `yaml:"queues"`
	SendLogs   sendLogsConfig  `yaml:"sendLogs"`
	Heartbeats heartbeatConfig `yaml:"heartbeats"`
	Signals    []customSignal  `yaml:"signals"`
}

type serversConfig struct {
	Count      int    `yaml:"count"`
	NamePrefix string `yaml:"namePrefix"`
	FirstPort  int    `yaml:"firstPort"`
}

type requestsConfig struct {
	PerServerPerMinute float64    `yaml:"perServerPerMinute"`
	URLs               int        `yaml:"urls"`
	URLSkew            float64    `yaml:"urlSkew"`
	Accounts           int        `yaml:"accounts"`
	AccountSkew        float64    `yaml:"accountSkew"`
	UsersPerAccount    int        `yaml:"usersPerAccount"`
	FastMS             valueRange `yaml:"fastMs"`
	SlowMS             valueRange `yaml:"slowMs"`
	SlowShare          float64    `yaml:"slowShare"`
}

type alertsConfig struct {
	PerMinute     float64        `yaml:"perMinute"`
	Levels        map[string]int `yaml:"levels"`
	Messages      []string       `yaml:"messages"`
	UniqueShare   float64        `yaml:"uniqueShare"`
	GroupKeyShare float64        `yaml:"groupKeyShare"`
	GroupKeys     int            `yaml:"groupKeys"`
}

type queuesConfig struct {
	Count     int           `yaml:"count"`
	Sources   int           `yaml:"sources"`
	Every     time.Duration `yaml:"every"`
	Max       float64       `yaml:"max"`
	WarnAbove float64       `yaml:"warnAbove"`
}

type sendLogsConfig struct {
	PerMinute float64    `yaml:"perMinute"`
	Mails     []mailKind `yaml:"mails"`
}

type mailKind struct {
	Type    string `yaml:"type"`
	Subject string `yaml:"subject"`
}

type heartbeatConfig struct {
	Every     time.Duration `yaml:"every"`
	Flap      bool          `yaml:"flap"`
	FlapEvery time.Duration `yaml:"flapEvery"`
	FlapDown  time.Duration `yaml:"flapDown"`
}

type customSignal struct {
	Name     string                `yaml:"name"`
	Type     string                `yaml:"type"`
	Rate     float64               `yaml:"rate"`
	Platform string                `yaml:"platform"`
	Dims     map[string]dimSpec    `yaml:"dims"`
	Values   map[string]valueRange `yaml:"values"`
	// PacketType is the legacy type field an info report is sent with.
	PacketType string `yaml:"packetType"`
	// Partial makes about one info report in three carry one entry, with
	// detail the full reports leave out, as a sender merging expects.
	Partial bool `yaml:"partial"`

	dimOrder   []string
	valueOrder []string
}

func (s *customSignal) UnmarshalYAML(n *yaml.Node) error {
	type plain customSignal
	if err := n.Decode((*plain)(s)); err != nil {
		return err
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		switch n.Content[i].Value {
		case "dims":
			s.dimOrder = mappingKeys(n.Content[i+1])
		case "values":
			s.valueOrder = mappingKeys(n.Content[i+1])
		}
	}
	return nil
}

func mappingKeys(n *yaml.Node) []string {
	var keys []string
	for i := 0; i+1 < len(n.Content); i += 2 {
		keys = append(keys, n.Content[i].Value)
	}
	return keys
}

func orderedKeys[V any](order []string, m map[string]V) []string {
	if len(order) == len(m) {
		return order
	}
	return sortedKeys(m)
}

type valueRange struct {
	Min, Max float64
}

func (r *valueRange) UnmarshalYAML(n *yaml.Node) error {
	var pair []float64
	if err := n.Decode(&pair); err != nil || len(pair) != 2 {
		return fmt.Errorf("line %d: a range is [min, max]", n.Line)
	}
	r.Min, r.Max = pair[0], pair[1]
	return nil
}

// dimSpec is a cardinality (`url: 40`), a fixed list of values, or
// `{ names: account }`: one name per value of another dimension, sent with it.
type dimSpec struct {
	Count  int
	Values []string
	Names  string
}

func (d *dimSpec) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.SequenceNode:
		return n.Decode(&d.Values)
	case yaml.MappingNode:
		var named struct {
			Names string `yaml:"names"`
		}
		if err := n.Decode(&named); err != nil || named.Names == "" {
			return fmt.Errorf("line %d: a named dimension is { names: <dimension> }", n.Line)
		}
		d.Names = named.Names
		return nil
	}
	if err := n.Decode(&d.Count); err != nil || d.Count <= 0 {
		return fmt.Errorf("line %d: a dimension is a positive count, a list of values or { names: <dimension> }", n.Line)
	}
	return nil
}

func (d *dimSpec) expand(name string) {
	if len(d.Values) > 0 || d.Names != "" {
		return
	}
	d.Values = make([]string, d.Count)
	for i := range d.Values {
		d.Values[i] = fmt.Sprintf("%s-%d", name, i+1)
	}
}

func loadConfig(path string) (config, error) {
	var c config
	if path == "" {
		return c, errors.New("-config is required (see soak.yaml)")
	}
	f, err := os.Open(path)
	if err != nil {
		return c, err
	}
	defer func() { _ = f.Close() }()
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil && !errors.Is(err, io.EOF) {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	if err := c.validate(); err != nil {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

func (c *config) validate() error {
	var errs []error
	fail := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	if c.Platform == "" {
		fail("platform is required")
	}
	if c.Protocol != "v0" && c.Protocol != "v1" {
		fail("protocol must be v0 or v1, not %q", c.Protocol)
	}
	if c.Duration < 0 {
		fail("duration must not be negative")
	}
	if c.Servers.Count <= 0 {
		fail("servers.count must be positive")
	}
	if c.Requests.PerServerPerMinute > 0 && (c.Requests.URLs <= 0 || c.Requests.Accounts <= 0 || c.Requests.UsersPerAccount <= 0) {
		fail("requests.urls, requests.accounts and requests.usersPerAccount must be positive")
	}
	if (c.Requests.PerServerPerMinute > 0 || c.Alerts.PerMinute > 0 || c.SendLogs.PerMinute > 0) &&
		(c.Requests.URLSkew < 1 || c.Requests.AccountSkew < 1) {
		fail("requests.urlSkew and requests.accountSkew must be 1 (even) or more")
	}
	if c.Requests.SlowShare < 0 || c.Requests.SlowShare > 1 {
		fail("requests.slowShare must be between 0 and 1")
	}
	for _, r := range []valueRange{c.Requests.FastMS, c.Requests.SlowMS} {
		if r.Min > r.Max {
			fail("requests latency range [%g, %g] is reversed", r.Min, r.Max)
		}
	}
	alertSignals := false
	for _, s := range c.Signals {
		alertSignals = alertSignals || s.Type == "alert"
	}
	if c.Alerts.PerMinute > 0 && len(c.Alerts.Messages) == 0 {
		fail("alerts.messages is empty")
	}
	if c.Alerts.PerMinute > 0 && c.Alerts.GroupKeyShare > 0 && c.Alerts.GroupKeys <= 0 {
		fail("alerts.groupKeys must be positive when groupKeyShare is set")
	}
	if c.Alerts.PerMinute > 0 || alertSignals {
		total := 0
		for level, w := range c.Alerts.Levels {
			switch level {
			case "ERROR", "WARN", "INFO", "DEBUG", "TRACE":
			default:
				fail("alerts.levels: unknown level %q", level)
			}
			total += max(w, 0)
		}
		if total == 0 {
			fail("alerts.levels needs at least one positive weight")
		}
	}
	if c.SendLogs.PerMinute > 0 && len(c.SendLogs.Mails) == 0 {
		fail("sendLogs.mails is empty")
	}
	if c.Queues.Count > 0 && (c.Queues.Every <= 0 || c.Queues.Sources <= 0) {
		fail("queues.every and queues.sources must be positive")
	}
	if c.Heartbeats.Every < 0 {
		fail("heartbeats.every must not be negative")
	}
	if c.Heartbeats.Flap && (c.Heartbeats.Every <= 0 || c.Heartbeats.FlapEvery <= 0 || c.Heartbeats.FlapDown <= 0) {
		fail("heartbeats.flap needs every, flapEvery and flapDown")
	}

	seen := map[string]bool{}
	for i := range c.Signals {
		s := &c.Signals[i]
		if s.Name == "" {
			fail("signals[%d]: name is required", i)
		}
		if seen[s.Name] {
			fail("signals: %q appears twice", s.Name)
		}
		seen[s.Name] = true
		switch s.Type {
		case "metric", "gauge", "status", "log", "alert":
		case "info":
			if s.PacketType == "" {
				fail("signals %q: an info report needs packetType", s.Name)
			}
		default:
			fail("signals %q: type must be metric, gauge, status, log, alert or info", s.Name)
		}
		if s.Rate <= 0 {
			fail("signals %q: rate must be positive", s.Name)
		}
		if s.Platform == "" {
			s.Platform = c.Platform
		}
		for name, d := range s.Dims {
			d.expand(name)
			s.Dims[name] = d
		}
		for name, d := range s.Dims {
			if d.Names == "" {
				continue
			}
			target, ok := s.Dims[d.Names]
			if !ok || target.Names != "" {
				fail("signals %q: dims.%s names %q, which is not a dimension with values of its own", s.Name, name, d.Names)
				continue
			}
			d.Values = make([]string, len(target.Values))
			for i, v := range target.Values {
				d.Values[i] = nameOf(v)
			}
			s.Dims[name] = d
		}
		for name, r := range s.Values {
			if r.Min > r.Max {
				fail("signals %q: values.%s range is reversed", s.Name, name)
			}
		}
		if (s.Type == "gauge" || s.Type == "status" || s.Type == "info") && len(s.Dims) == 0 {
			fail("signals %q: a %s needs at least one dimension to label it", s.Name, s.Type)
		}
	}
	return errors.Join(errs...)
}

// nameOf turns account-17 into "Soak Account 17".
func nameOf(value string) string {
	words := strings.ReplaceAll(value, "-", " ")
	if words == "" {
		return "Soak"
	}
	return "Soak " + strings.ToUpper(words[:1]) + words[1:]
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
