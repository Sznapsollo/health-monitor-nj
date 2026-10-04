// Package legacy translates the flat legacy packets ("protocol v0") into the
// v1 envelopes the core understands. It reads native names; a sender's own
// type and field names are translated by the platform's mapping.yaml, so
// nothing outside that file knows what the sender calls them.
package legacy

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/Sznapsollo/health-monitor-nj/internal/protocol"
)

// Platform is the name this adapter labels its packets with when the
// deployment does not override it.
const Platform = "example"

// Signal names the adapter produces. They must match platforms/example/signals.yaml.
const (
	SignalRequests = "requests"
	SignalJobs     = "jobs"
	SignalServers  = "servers"
	SignalSendLogs = "sendLogs"
	// SignalDailyLogs is one searchable row per request, which the old server
	// kept in memory and flushed to partial files.
	SignalDailyLogs = "dailyLogs"
	// SignalQueues is the old "Wykres zapełnienia kolejek".
	SignalQueues = "jobQueuesLoad"
	// SignalVPNUsers is the old VPN users chart.
	SignalVPNUsers = "vpnUsersLoad"
	SignalAlerts   = "alerts"
)

// maxLearnedAccounts bounds the account -> accountName map, which is built
// from request packets so job packets can be labelled the same way.
const maxLearnedAccounts = 50000

// Options configure the adapter.
type Options struct {
	// Platform labels the packets; empty means "example".
	Platform string
	// DailyLogs records one searchable row per request, as the old server's
	// in-memory dailyLogs buffer did. It doubles the work per request, so a
	// high-volume platform leaves it off and keeps only the charts.
	DailyLogs bool
	// Mapping translates the sender's names; nil reads native names only.
	Mapping *Mapping
	// InfoSignal names the info signal keeping packets of these types, as
	// the sender calls them and after the mapping; nil keeps none.
	InfoSignal func(types ...string) string
	// MetricSignal names the timeseries charting a packet type the adapter
	// does not read; its top-level fields become dims and values.
	MetricSignal func(types ...string) string
	// OnUnknownType hears of a packet type nothing reads, which becomes an
	// alert, with the packet as it arrived; nil ignores them.
	OnUnknownType func(packetType string, raw []byte)
}

// Adapter converts v0 packets. It is safe for concurrent use by the readers.
type Adapter struct {
	platform  string
	dailyLogs bool
	mapping   atomic.Pointer[Mapping]
	info      func(types ...string) string
	metric    func(types ...string) string
	unknown   func(packetType string, raw []byte)

	mu           sync.RWMutex
	accountNames map[string]string
}

// New builds an adapter labelling packets with platform, defaulting to
// "example".
func New(platform string) *Adapter {
	return NewWithOptions(Options{Platform: platform})
}

// NewWithOptions builds an adapter with everything configurable set.
func NewWithOptions(o Options) *Adapter {
	if o.Platform == "" {
		o.Platform = Platform
	}
	a := &Adapter{
		platform:     o.Platform,
		dailyLogs:    o.DailyLogs,
		info:         o.InfoSignal,
		metric:       o.MetricSignal,
		unknown:      o.OnUnknownType,
		accountNames: make(map[string]string),
	}
	a.mapping.Store(o.Mapping)
	return a
}

// SetMapping replaces the name translation; nil reads native names only.
func (a *Adapter) SetMapping(m *Mapping) { a.mapping.Store(m) }

// v0 is the flat packet every legacy sender builds. `additionalData` is
// merged into the root before sending, so a sender can add or override any
// key here; the extras are picked up separately for alerts and logs.
// v0LoadItem is one bar of a queue-load or VPN-users chart.
type v0LoadItem struct {
	Name    string  `json:"name"`
	Value   float64 `json:"value"`
	Warning bool    `json:"warning"`
}

type v0 struct {
	Type          string             `json:"type"`
	Start         protocol.Timestamp `json:"start"`
	ExecutionTime float64            `json:"executionTime"`
	Message       string             `json:"message"`
	Level         string             `json:"level"`
	ServerName    string             `json:"serverName"`
	Port          string             `json:"port"`
	User          string             `json:"user"`
	Account       string             `json:"account"`
	AccountName   string             `json:"accountName"`
	IPAddress     string             `json:"ipAddress"`
	URL           string             `json:"url"`
	GroupKey      string             `json:"groupKey"`
	Origin        string             `json:"origin"`

	// jobReport
	JobName    string  `json:"jobName"`
	ItemsCount float64 `json:"itemsCount"`

	// customLog
	LogPrefix string `json:"logPrefix"`

	// jobQueuesLoad and vpnUsersLoad carry a list of labelled counts. The old
	// server read exactly these field names (Server.groovy:2379-2409).
	QueueLoads []v0LoadItem `json:"queueLoads"`
	VPNUsers   []v0LoadItem `json:"vpnUsers"`
	Source     string       `json:"source"`

	// configServerListStatus
	StartDate    string          `json:"startDate"`
	Branch       string          `json:"branch"`
	Build        string          `json:"build"`
	ServerConfig json.RawMessage `json:"serverConfig"`
}

// Adapt turns one v0 datagram into zero or more v1 packets.
func (a *Adapter) Adapt(raw []byte) ([]protocol.Packet, error) {
	packets, _, err := a.AdaptTyped(raw)
	return packets, err
}

// AdaptTyped is Adapt that also returns the packet's type as the sender gave
// it, after the mapping: "request", "customLog", "configServerListStatus"…
func (a *Adapter) AdaptTyped(raw []byte) ([]protocol.Packet, string, error) {
	m := a.mapping.Load()
	raw = m.rename(raw)
	var p v0
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, "", fmt.Errorf("legacy: decode v0: %w", err)
	}
	sent := p.Type
	p.Type = m.packetType(p.Type)
	if p.Type == "" {
		p.Type = TypeCustomWarning
	}

	header := protocol.Header{
		V:        protocol.Version,
		Platform: a.platform,
		Source:   p.ServerName,
		TS:       p.Start,
	}

	if a.info != nil {
		if name := a.info(sent, p.Type); name != "" {
			return []protocol.Packet{infoPacket(header, name, p.ServerName, raw)}, p.Type, nil
		}
	}

	switch p.Type {
	case TypeRequest:
		header.T = protocol.TypeMetric
		a.learnAccount(p.Account, p.AccountName)
		m := &protocol.Metric{
			Header: header,
			Signal: SignalRequests,
			Dims: dims(map[string]string{
				"account":     p.Account,
				"accountName": p.AccountName,
				"url":         p.URL,
				"port":        p.Port,
				"user":        p.User,
				"ipAddress":   p.IPAddress,
				"serverName":  p.ServerName,
			}),
			Values: map[string]float64{"count": 1, "ms": p.ExecutionTime},
		}
		out := []protocol.Packet{{Header: header, Metric: m}}
		if a.dailyLogs {
			out = append(out, dailyLogOf(header, p))
		}
		return out, p.Type, nil

	case TypeJobReport:
		header.T = protocol.TypeMetric
		count := p.ItemsCount
		if count <= 0 {
			// The old server counted a report with no itemsCount as one item.
			count = 1
		}
		accountName := p.AccountName
		if accountName == "" {
			accountName = a.accountNameFor(p.Account)
		}
		m := &protocol.Metric{
			Header: header,
			Signal: SignalJobs,
			Dims: dims(map[string]string{
				"jobName":     p.JobName,
				"account":     p.Account,
				"accountName": accountName,
			}),
			Values: map[string]float64{"count": count, "ms": p.ExecutionTime},
		}
		return []protocol.Packet{{Header: header, Metric: m}}, p.Type, nil

	case TypeServerStatus, TypeJobsServerStatus, TypeMailServerStatus:
		header.T = protocol.TypeStatus
		payload := map[string]any{
			"branch":    p.Branch,
			"build":     p.Build,
			"startedAt": p.StartDate,
			"kind":      statusKind(p.Type),
		}
		if len(p.ServerConfig) > 0 {
			var cfg map[string]any
			if err := json.Unmarshal(p.ServerConfig, &cfg); err == nil {
				payload["serverConfig"] = cfg
			}
		}
		key := p.ServerName
		if key == "" {
			key = "unknown"
		}
		s := &protocol.Status{Header: header, Signal: SignalServers, Key: key, Payload: payload}
		return []protocol.Packet{{Header: header, Status: s}}, p.Type, nil

	case TypeQueuesLoad, TypeVPNUsersLoad:
		// The old queue-load and VPN-users charts are gauges: the latest
		// labelled values per source, no history. An empty list is valid
		// data — "nothing queued" — and still refreshes the chart.
		header.T = protocol.TypeGauge
		signalName, items := SignalQueues, p.QueueLoads
		if p.Type == TypeVPNUsersLoad {
			signalName, items = SignalVPNUsers, p.VPNUsers
		}
		points := make([]protocol.GaugePoint, 0, len(items))
		for _, item := range items {
			points = append(points, protocol.GaugePoint{
				Label: item.Name, Value: item.Value, Warn: item.Warning,
			})
		}
		// vpnUsersLoad carries its own source; queue loads are per server.
		source := p.Source
		if source == "" {
			source = p.ServerName
		}
		if source == "" {
			source = "default"
		}
		header.Source = source
		g := &protocol.Gauge{Header: header, Signal: signalName, Points: points}
		return []protocol.Packet{{Header: header, Gauge: g}}, p.Type, nil

	case TypeCustomLog:
		header.T = protocol.TypeLog
		signalName := p.LogPrefix
		if signalName == "" {
			signalName = "logs"
		}
		// The searchable fields have to travel with the row: `extras` leaves
		// out everything the envelope already names, but a log is looked up
		// by account, user and url as often as by its own free-form keys.
		data := extras(raw)
		if data == nil {
			data = map[string]any{}
		}
		for key, value := range map[string]string{
			"account":     p.Account,
			"accountName": p.AccountName,
			"user":        p.User,
			"url":         p.URL,
			"serverName":  p.ServerName,
		} {
			if value != "" {
				data[key] = value
			}
		}
		if p.Message != "" {
			data["message"] = p.Message
		}

		l := &protocol.Log{
			Header: header,
			Signal: signalName,
			Key:    p.Account,
			Level:  level(p.Level),
			Data:   data,
		}
		return []protocol.Packet{{Header: header, Log: l}}, p.Type, nil

	default:
		// Everything else is free text with a level: `customWarning` and
		// whatever a sender invents by overriding `type`.
		if a.metric != nil && p.Type != TypeCustomWarning {
			if name := a.metric(sent, p.Type); name != "" {
				header.T = protocol.TypeMetric
				dims, values := FlatFields(raw)
				m := &protocol.Metric{Header: header, Signal: name, Dims: dims, Values: values}
				return []protocol.Packet{{Header: header, Metric: m}}, p.Type, nil
			}
		}
		if a.unknown != nil && p.Type != TypeCustomWarning {
			a.unknown(sent, raw)
		}
		header.T = protocol.TypeAlert
		al := &protocol.Alert{
			Header:   header,
			Signal:   SignalAlerts,
			Level:    level(p.Level),
			Category: category(p.Level, m.originWord(p.Origin)),
			Message:  p.Message,
			GroupKey: p.GroupKey,
			Data:     extras(raw),
		}
		return []protocol.Packet{{Header: header, Alert: al}}, p.Type, nil
	}
}

// infoPacket carries the whole packet to an info signal, one entry per sender.
func infoPacket(header protocol.Header, name, serverName string, raw []byte) protocol.Packet {
	var content map[string]any
	_ = json.Unmarshal(raw, &content)
	key := serverName
	if key == "" {
		key = "unknown"
	}
	header.T = protocol.TypeStatus
	s := &protocol.Status{Header: header, Signal: name, Key: key, Payload: map[string]any{"kind": "info"}, Content: content}
	return protocol.Packet{Header: header, Status: s}
}

// FlatFields reads a packet's top-level scalars as dimensions, and its
// numbers also as values, so a chart can pick either.
func FlatFields(raw []byte) (map[string]string, map[string]float64) {
	var all map[string]any
	_ = json.Unmarshal(raw, &all)
	dims := make(map[string]string, len(all))
	values := map[string]float64{}
	for k, v := range all {
		switch v := v.(type) {
		case string:
			if v != "" {
				dims[k] = v
			}
		case bool:
			dims[k] = strconv.FormatBool(v)
		case float64:
			dims[k] = strconv.FormatFloat(v, 'f', -1, 64)
			values[k] = v
		}
	}
	return dims, values
}

// dailyLogOf is the searchable row for one request: the same fields the old
// server's dailyLogs buffer held.
func dailyLogOf(header protocol.Header, p v0) protocol.Packet {
	logHeader := header
	logHeader.T = protocol.TypeLog
	return protocol.Packet{
		Header: logHeader,
		Log: &protocol.Log{
			Header: logHeader,
			Signal: SignalDailyLogs,
			Key:    p.Account,
			Level:  level(p.Level),
			Data: map[string]any{
				"account":       p.Account,
				"accountName":   p.AccountName,
				"user":          p.User,
				"url":           p.URL,
				"ipAddress":     p.IPAddress,
				"serverName":    p.ServerName,
				"port":          p.Port,
				"executionTime": p.ExecutionTime,
			},
		},
	}
}

func statusKind(v0Type string) string {
	switch v0Type {
	case TypeJobsServerStatus:
		return "jobs"
	case TypeMailServerStatus:
		return "mail"
	default:
		return "app"
	}
}

// level maps the Java enum name onto a protocol level. The old server also
// knew NOTIFY, which no legacy sender emits but which costs nothing to
// accept.
func level(name string) protocol.Level {
	switch strings.ToUpper(strings.TrimSpace(name)) {
	case "ERROR":
		return protocol.LevelError
	case "WARN", "WARNING":
		return protocol.LevelWarn
	case "DEBUG":
		return protocol.LevelDebug
	case "TRACE":
		return protocol.LevelTrace
	case "NOTIFY", "INFO", "":
		return protocol.LevelInfo
	default:
		return protocol.LevelInfo
	}
}

// category is the level in words plus where the packet came from, as the old
// server worded it.
func category(levelName, where string) string {
	var word string
	switch strings.ToUpper(strings.TrimSpace(levelName)) {
	case "ERROR":
		word = "Error"
	case "WARN", "WARNING":
		word = "Warning"
	case "DEBUG":
		word = "Debug"
	case "TRACE":
		word = "Trace"
	case "NOTIFY":
		word = "Notify"
	default:
		word = "Info"
	}
	return word + " " + where
}

// dims drops empty values so a missing field never becomes a chart series.
func dims(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		if v != "" {
			out[k] = v
		}
	}
	return out
}

// known are the fields the struct above already carries, so the free-form
// extras of an alert or log do not repeat them.
var known = map[string]bool{
	"type": true, "start": true, "executionTime": true, "message": true,
	"level": true, "serverName": true, "port": true, "user": true,
	"account": true, "accountName": true, "ipAddress": true, "url": true,
	"groupKey": true, "origin": true, "jobName": true, "itemsCount": true,
	"logPrefix": true, "startDate": true, "branch": true, "build": true,
	"serverConfig": true,
}

// extras collects the keys a sender merged in through additionalData. Only
// alerts and logs pay this cost; the high-volume metric path never does.
func extras(raw []byte) map[string]any {
	var all map[string]any
	if err := json.Unmarshal(raw, &all); err != nil {
		return nil
	}
	out := make(map[string]any, len(all))
	for k, v := range all {
		if !known[k] {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (a *Adapter) learnAccount(account, name string) {
	if account == "" || name == "" || account == name {
		return
	}
	a.mu.RLock()
	have, ok := a.accountNames[account]
	a.mu.RUnlock()
	if ok && have == name {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.accountNames) >= maxLearnedAccounts {
		return
	}
	a.accountNames[account] = name
}

func (a *Adapter) accountNameFor(account string) string {
	if account == "" {
		return ""
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.accountNames[account]
}
