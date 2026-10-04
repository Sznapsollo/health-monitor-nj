package main

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"strconv"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/protocol"
)

const v0Time = "2006-01-02T15:04:05.000-0700"

type server struct {
	name string
	port string
}

type traffic struct {
	cfg     config
	v1      bool
	servers []server
	urls    []string
	started time.Time
}

func newTraffic(cfg config, v1 bool, started time.Time) *traffic {
	t := &traffic{cfg: cfg, v1: v1, started: started}
	for i := range cfg.Servers.Count {
		t.servers = append(t.servers, server{
			name: fmt.Sprintf("%s%d", cfg.Servers.NamePrefix, i+1),
			port: strconv.Itoa(cfg.Servers.FirstPort + i),
		})
	}
	t.urls = urlList(cfg.Requests.URLs)
	return t
}

var (
	urlResources = []string{"offers", "candidates", "applications", "accounts", "invoices", "messages", "reports", "files"}
	urlActions   = []string{"list", "get", "save", "search", "export"}
)

func urlList(n int) []string {
	out := make([]string, 0, n)
	for i := range n {
		combo := i % (len(urlResources) * len(urlActions))
		u := fmt.Sprintf("/api/%s/%s", urlResources[combo%len(urlResources)], urlActions[combo/len(urlResources)])
		if round := i / (len(urlResources) * len(urlActions)); round > 0 {
			u += "/" + strconv.Itoa(round)
		}
		out = append(out, u)
	}
	return out
}

type account struct {
	id, name, user, ip string
}

func (t *traffic) account(rng *rand.Rand) account {
	n := skewed(rng, max(t.cfg.Requests.Accounts, 1), t.cfg.Requests.AccountSkew)
	return account{
		id:   strconv.Itoa(100000 + n),
		name: fmt.Sprintf("Soak Account %d", n),
		user: fmt.Sprintf("user%d.%d@example.test", n, rng.Intn(t.cfg.Requests.UsersPerAccount)),
		ip:   fmt.Sprintf("10.%d.%d.%d", n>>16&0xff, n>>8&0xff, n&0xff),
	}
}

// listSkew is how strongly the earlier entries of a fixed list win.
const listSkew = 2

// skewed picks from [0, n) with low indices more likely: power 1 is even,
// 2 lets a few dominate, and each step up concentrates it further.
func skewed(rng *rand.Rand, n int, power float64) int {
	return min(int(float64(n)*math.Pow(rng.Float64(), power)), n-1)
}

func (t *traffic) latency(rng *rand.Rand) float64 {
	r := t.cfg.Requests.FastMS
	if rng.Float64() < t.cfg.Requests.SlowShare {
		r = t.cfg.Requests.SlowMS
	}
	u := rng.Float64()
	return math.Round(r.Min + (r.Max-r.Min)*u*u)
}

func (t *traffic) header(typ protocol.Type, source string, now time.Time) protocol.Header {
	return protocol.Header{V: protocol.Version, T: typ, Platform: t.cfg.Platform, Source: source, TS: protocol.NewTimestamp(now)}
}

func (t *traffic) request(rng *rand.Rand, srv server, now time.Time) []byte {
	acc := t.account(rng)
	url := t.urls[skewed(rng, len(t.urls), t.cfg.Requests.URLSkew)]
	ms := t.latency(rng)
	if t.v1 {
		return mustJSON(protocol.Metric{
			Header: t.header(protocol.TypeMetric, srv.name, now),
			Signal: "requests",
			Dims: map[string]string{
				"account": acc.id, "accountName": acc.name, "url": url, "port": srv.port,
				"user": acc.user, "ipAddress": acc.ip, "serverName": srv.name,
			},
			Values: map[string]float64{"count": 1, "ms": ms},
		})
	}
	return mustJSON(map[string]any{
		"type": "request", "start": now.Format(v0Time), "executionTime": ms, "level": "DEBUG",
		"serverName": srv.name, "port": srv.port, "user": acc.user, "account": acc.id,
		"accountName": acc.name, "ipAddress": acc.ip, "url": url,
	})
}

func (t *traffic) alert(rng *rand.Rand, now time.Time) []byte {
	a := t.cfg.Alerts
	srv := t.servers[rng.Intn(len(t.servers))]
	acc := t.account(rng)
	level := weighted(rng, a.Levels)
	message := a.Messages[skewed(rng, len(a.Messages), listSkew)]
	groupKey := ""
	switch {
	case rng.Float64() < a.GroupKeyShare:
		groupKey = fmt.Sprintf("soak/group-%d", rng.Intn(a.GroupKeys)+1)
		message = fmt.Sprintf("%s (ref %06d)", message, rng.Intn(1000000))
	case rng.Float64() < a.UniqueShare:
		message = fmt.Sprintf("%s (ref %06d)", message, rng.Intn(1000000))
	}
	origin := "app"
	if rng.Intn(4) == 0 {
		origin = "job"
	}
	if t.v1 {
		return mustJSON(protocol.Alert{
			Header:   t.header(protocol.TypeAlert, srv.name, now),
			Signal:   "alerts",
			Level:    protocol.Level(level),
			Category: alertCategory(level, origin),
			Message:  message,
			GroupKey: groupKey,
			Data:     map[string]any{"account": acc.id, "user": acc.user, "serverName": srv.name},
		})
	}
	return mustJSON(map[string]any{
		"type": "customWarning", "start": now.Format(v0Time), "level": level, "message": message,
		"groupKey": groupKey, "origin": origin, "serverName": srv.name, "port": srv.port,
		"user": acc.user, "account": acc.id, "accountName": acc.name,
	})
}

func alertCategory(level, origin string) string {
	words := map[string]string{"ERROR": "Error", "WARN": "Warning", "INFO": "Info", "DEBUG": "Debug", "TRACE": "Trace"}
	where := "APP"
	if origin == "job" {
		where = "JOB"
	}
	return words[level] + " " + where
}

func (t *traffic) sendLog(rng *rand.Rand, now time.Time) []byte {
	acc := t.account(rng)
	mails := t.cfg.SendLogs.Mails
	mail := mails[skewed(rng, len(mails), listSkew)]
	to := fmt.Sprintf("recipient%d@example.test", rng.Intn(20000))
	subject := fmt.Sprintf("%s #%d", mail.Subject, rng.Intn(100000))
	fields := map[string]any{
		"method": "sendMail", "emailType": mail.Type, "fromMail": "do_not_reply@example.test",
		"whichSender": "PRIMARY", "to": to, "subject": subject, "redirected": false,
	}
	if t.v1 {
		fields["account"], fields["accountName"], fields["user"] = acc.id, acc.name, acc.user
		return mustJSON(protocol.Log{
			Header: t.header(protocol.TypeLog, "", now),
			Signal: "sendLogs",
			Key:    acc.id,
			Level:  protocol.LevelInfo,
			Data:   fields,
		})
	}
	fields["type"], fields["logPrefix"], fields["start"], fields["level"] = "customLog", "sendLogs", now.Format(v0Time), "INFO"
	fields["account"], fields["accountName"], fields["user"], fields["skipRequest"] = acc.id, acc.name, acc.user, true
	return mustJSON(fields)
}

func (t *traffic) heartbeat(srv server, now time.Time) []byte {
	started := t.started.Format(v0Time)
	if t.v1 {
		return mustJSON(protocol.Status{
			Header: t.header(protocol.TypeStatus, srv.name, now),
			Signal: "servers",
			Key:    srv.name,
			Payload: map[string]any{
				"branch": "soak", "build": "soak", "startedAt": started, "kind": "app",
				"serverConfig": map[string]any{"host": srv.name, "port": srv.port},
			},
		})
	}
	return mustJSON(map[string]any{
		"type": "configServerListStatus", "start": now.Format(v0Time), "level": "DEBUG",
		"serverName": srv.name, "port": srv.port, "startDate": started, "branch": "soak", "build": "soak",
		"serverConfig": map[string]any{"host": srv.name, "port": srv.port, "branch": "soak", "build": "soak"},
	})
}

// drift is a value that wanders towards a target and picks a new target now
// and then, so a gauge moves like a queue filling and draining.
type drift struct {
	value, target float64
}

func (d *drift) step(rng *rand.Rand, limit float64) float64 {
	if rng.Float64() < 0.05 {
		u := rng.Float64()
		d.target = limit * u * u * u
	}
	d.value += (d.target-d.value)*0.2 + rng.NormFloat64()*limit*0.005
	d.value = math.Max(0, math.Min(limit, d.value))
	return math.Round(d.value)
}

type queueSource struct {
	name   string
	queues []drift
}

func (t *traffic) queueSources() []*queueSource {
	q := t.cfg.Queues
	out := make([]*queueSource, 0, q.Sources)
	for i := range q.Sources {
		name := "soak-jobs"
		if q.Sources > 1 {
			name = fmt.Sprintf("soak-jobs-%d", i+1)
		}
		out = append(out, &queueSource{name: name, queues: make([]drift, q.Count)})
	}
	return out
}

func (t *traffic) queueReport(rng *rand.Rand, src *queueSource, now time.Time) []byte {
	q := t.cfg.Queues
	points := make([]protocol.GaugePoint, len(src.queues))
	for i := range src.queues {
		v := src.queues[i].step(rng, q.Max)
		points[i] = protocol.GaugePoint{Label: fmt.Sprintf("soak.queue.%03d", i+1), Value: v, Warn: q.WarnAbove > 0 && v > q.WarnAbove}
	}
	if t.v1 {
		return mustJSON(protocol.Gauge{Header: t.header(protocol.TypeGauge, src.name, now), Signal: "jobQueuesLoad", Points: points})
	}
	items := make([]map[string]any, len(points))
	for i, p := range points {
		items[i] = map[string]any{"name": p.Label, "value": p.Value, "warning": p.Warn}
	}
	return mustJSON(map[string]any{
		"type": "jobQueuesLoad", "start": now.Format(v0Time), "serverName": src.name, "queueLoads": items,
	})
}

type customState struct {
	sig    *customSignal
	dims   []string
	values []string
	gauges map[string]*drift
	// turn is the next key a status reports for, so every key keeps reporting.
	turn int
}

func newCustomState(s *customSignal) *customState {
	return &customState{
		sig: s, dims: orderedKeys(s.dimOrder, s.Dims), values: orderedKeys(s.valueOrder, s.Values),
		gauges: map[string]*drift{},
	}
}

func (t *traffic) custom(rng *rand.Rand, st *customState, now time.Time) []byte {
	s := st.sig
	source := t.servers[rng.Intn(len(t.servers))].name
	switch s.Type {
	case "gauge":
		// The monitor adds sources together per label, so one gauge must keep one source.
		source = t.servers[0].name
	case "info":
		// One sender, so it reports often enough never to look offline.
		return t.customInfo(rng, st, t.servers[0].name, now)
	}
	header := t.header(protocol.Type(s.Type), source, now)
	header.Platform = s.Platform

	dims := make(map[string]string, len(st.dims))
	picked := make(map[string]int, len(st.dims))
	for i, name := range st.dims {
		if d := s.Dims[name]; d.Names == "" {
			picked[name] = skewed(rng, len(d.Values), listSkew)
			if s.Type == "status" && i == 0 {
				picked[name] = st.turn % len(d.Values)
				st.turn++
			}
			dims[name] = d.Values[picked[name]]
		}
	}
	for _, name := range st.dims {
		if d := s.Dims[name]; d.Names != "" {
			dims[name] = d.Values[picked[d.Names]]
		}
	}
	values := make(map[string]float64, len(st.values))
	for _, name := range st.values {
		values[name] = draw(rng, s.Values[name])
	}
	data := make(map[string]any, len(dims)+len(values))
	for k, v := range dims {
		data[k] = v
	}
	for k, v := range values {
		data[k] = v
	}
	first := ""
	if len(st.dims) > 0 {
		first = dims[st.dims[0]]
	}

	switch s.Type {
	case "metric":
		return mustJSON(protocol.Metric{Header: header, Signal: s.Name, Dims: dims, Values: values})
	case "gauge":
		return mustJSON(protocol.Gauge{Header: header, Signal: s.Name, Points: t.customGauge(rng, st)})
	case "status":
		return mustJSON(protocol.Status{Header: header, Signal: s.Name, Key: first, Payload: data})
	case "log":
		return mustJSON(protocol.Log{Header: header, Signal: s.Name, Key: first, Level: protocol.LevelInfo, Data: data})
	default:
		level := weighted(rng, t.cfg.Alerts.Levels)
		return mustJSON(protocol.Alert{
			Header: header, Signal: s.Name, Level: protocol.Level(level), Category: s.Name,
			Message: fmt.Sprintf("%s: %s", s.Name, first), Data: data,
		})
	}
}

// customInfo is a legacy report of the signal's packetType, listing every
// value of the first dimension with a heartbeat and the drawn values; a
// partial one lists a single value with the detail the full ones leave out.
func (t *traffic) customInfo(rng *rand.Rand, st *customState, source string, now time.Time) []byte {
	s := st.sig
	dim := st.dims[0]
	entry := func(name string) map[string]any {
		e := map[string]any{"name": name, "heartBeat": now.UnixMilli()}
		for _, v := range st.values {
			e[v] = draw(rng, s.Values[v])
		}
		return e
	}
	list := map[string]any{}
	names := s.Dims[dim].Values
	if s.Partial && rng.Intn(3) == 0 {
		name := names[rng.Intn(len(names))]
		e := entry(name)
		e["config"] = map[string]any{"version": fmt.Sprintf("1.%d.%d", rng.Intn(9), rng.Intn(20)), "registeredAt": now.Format(v0Time)}
		list[name] = e
	} else {
		for _, name := range names {
			list[name] = entry(name)
		}
	}
	return mustJSON(map[string]any{
		"type": s.PacketType, "start": now.Format(v0Time), "serverName": source, dim + "StatusMap": list,
	})
}

// customGauge reports every value of the first dimension as a label, each
// drifting within the first value range.
func (t *traffic) customGauge(rng *rand.Rand, st *customState) []protocol.GaugePoint {
	limit := valueRange{0, 100}
	if len(st.values) > 0 {
		limit = st.sig.Values[st.values[0]]
	}
	labels := st.sig.Dims[st.dims[0]].Values
	points := make([]protocol.GaugePoint, len(labels))
	for i, label := range labels {
		d, ok := st.gauges[label]
		if !ok {
			d = &drift{value: limit.Min, target: limit.Min}
			st.gauges[label] = d
		}
		points[i] = protocol.GaugePoint{Label: label, Value: limit.Min + d.step(rng, limit.Max-limit.Min)}
	}
	return points
}

// draw is uniform in the range, and whole when both ends are whole so a
// `count: [1, 3]` never reports 2.37.
func draw(rng *rand.Rand, r valueRange) float64 {
	v := r.Min + rng.Float64()*(r.Max-r.Min)
	if r.Min == math.Trunc(r.Min) && r.Max == math.Trunc(r.Max) {
		return math.Min(math.Floor(v+0.5), r.Max)
	}
	return v
}

func weighted(rng *rand.Rand, weights map[string]int) string {
	keys := sortedKeys(weights)
	total := 0
	for _, k := range keys {
		total += max(weights[k], 0)
	}
	n := rng.Intn(max(total, 1))
	for _, k := range keys {
		n -= max(weights[k], 0)
		if n < 0 {
			return k
		}
	}
	return keys[len(keys)-1]
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
