package core

import (
	"encoding/json"
	"sort"
	"strconv"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/protocol"
	"github.com/Sznapsollo/health-monitor-nj/internal/state"
)

const (
	maxCandidates     = 100
	maxCandidateDims  = 30
	maxDimSamples     = 10
	maxCandidateVals  = 20
	candidateMinutes  = 30
	maxDimSampleBytes = 120
	// maxSampleBytes caps the last packet kept to show; a bigger one is not kept.
	maxSampleBytes = 64 << 10
)

// Candidate is what the designer knows about a signal arriving without a definition.
type Candidate struct {
	Platform string              `json:"platform"`
	Signal   string              `json:"signal"`
	Kind     string              `json:"kind"`
	Count    int64               `json:"count"`
	First    time.Time           `json:"first"`
	Last     time.Time           `json:"last"`
	Dims     map[string][]string `json:"dims"`
	Values   map[string]Range    `json:"values"`
	Minutes  []CandidateMinute   `json:"minutes"`
}

// Range is the smallest and largest value a field has carried.
type Range struct {
	Min float64 `json:"min"`
	Max float64 `json:"max"`
}

// CandidateMinute keeps value sums rather than counts, so any count/ms mapping can be previewed.
type CandidateMinute struct {
	Minute  int64              `json:"minute"`
	Packets int64              `json:"packets"`
	Sums    map[string]float64 `json:"sums"`
}

type candidate struct {
	Candidate
	seen    map[string]map[string]bool
	minutes map[int64]*CandidateMinute
	// sample is the last packet as it arrived.
	sample json.RawMessage
}

func (cand *candidate) keep(sample []byte) {
	if len(sample) <= maxSampleBytes {
		cand.sample = append(cand.sample[:0], sample...)
	}
}

// CandidateSample is the last packet an undefined signal arrived with.
func (c *Core) CandidateSample(platform, sig string) (json.RawMessage, bool) {
	c.candMu.Lock()
	defer c.candMu.Unlock()
	cand, ok := c.candidates[candidateKey(platform, sig)]
	if !ok || len(cand.sample) == 0 {
		return nil, false
	}
	return append(json.RawMessage(nil), cand.sample...), true
}

func candidateKey(platform, sig string) string { return platform + "\x00" + sig }

func (c *Core) observeCandidate(p protocol.Packet, at time.Time) {
	if p.Metric == nil {
		return
	}
	m := p.Metric
	c.candMu.Lock()
	defer c.candMu.Unlock()
	if c.candidates == nil {
		c.candidates = map[string]*candidate{}
	}
	key := candidateKey(p.Platform, m.Signal)
	cand, ok := c.candidates[key]
	if !ok {
		if len(c.candidates) >= maxCandidates {
			c.dropStalestCandidateLocked()
		}
		cand = &candidate{
			Candidate: Candidate{
				Platform: p.Platform, Signal: m.Signal, Kind: string(protocol.TypeMetric),
				First: at, Dims: map[string][]string{}, Values: map[string]Range{},
			},
			seen:    map[string]map[string]bool{},
			minutes: map[int64]*CandidateMinute{},
		}
		c.candidates[key] = cand
	}
	cand.Count++
	cand.Last = at
	if sample, err := json.Marshal(m); err == nil {
		cand.keep(sample)
	}

	ts := p.TS.Time
	if ts.IsZero() {
		ts = at
	}
	cand.observe(m.Dims, m.Values, state.MinuteOf(ts))
}

func (cand *candidate) observe(dims map[string]string, values map[string]float64, minute int64) {
	for dim, value := range dims {
		samples, ok := cand.seen[dim]
		if !ok {
			if len(cand.seen) >= maxCandidateDims {
				continue
			}
			samples = map[string]bool{}
			cand.seen[dim] = samples
			cand.Dims[dim] = []string{}
		}
		if len(samples) >= maxDimSamples || samples[value] || len(value) > maxDimSampleBytes {
			continue
		}
		samples[value] = true
		cand.Dims[dim] = append(cand.Dims[dim], value)
	}

	bucket, ok := cand.minutes[minute]
	if !ok {
		bucket = &CandidateMinute{Minute: minute, Sums: map[string]float64{}}
		cand.minutes[minute] = bucket
		for old := range cand.minutes {
			if old <= minute-candidateMinutes {
				delete(cand.minutes, old)
			}
		}
	}
	bucket.Packets++
	for field, v := range values {
		r, ok := cand.Values[field]
		if !ok {
			if len(cand.Values) >= maxCandidateVals {
				continue
			}
			r = Range{Min: v, Max: v}
		}
		r.Min, r.Max = min(r.Min, v), max(r.Max, v)
		cand.Values[field] = r
		bucket.Sums[field] += v
	}
}

// NoteUnknownType lists a legacy packet type the monitor does not read, which
// arrives as an alert until an info signal names it; its top-level fields
// are listed and the packet kept to show.
func (c *Core) NoteUnknownType(platform, packetType string, raw []byte) {
	at := c.now()
	var top map[string]json.RawMessage
	_ = json.Unmarshal(raw, &top)
	c.candMu.Lock()
	defer c.candMu.Unlock()
	if c.candidates == nil {
		c.candidates = map[string]*candidate{}
	}
	key := candidateKey(platform, packetType)
	cand, ok := c.candidates[key]
	if !ok {
		if len(c.candidates) >= maxCandidates {
			c.dropStalestCandidateLocked()
		}
		cand = &candidate{
			Candidate: Candidate{
				Platform: platform, Signal: packetType, Kind: "info",
				First: at, Dims: map[string][]string{}, Values: map[string]Range{},
			},
			seen:    map[string]map[string]bool{},
			minutes: map[int64]*CandidateMinute{},
		}
		c.candidates[key] = cand
	}
	cand.Count++
	cand.Last = at
	cand.keep(raw)
	dims, values := map[string]string{}, map[string]float64{}
	for f, v := range top {
		var scalar any
		_ = json.Unmarshal(v, &scalar)
		switch scalar := scalar.(type) {
		case string:
			dims[f] = scalar
		case bool:
			dims[f] = strconv.FormatBool(scalar)
		case float64:
			dims[f] = strconv.FormatFloat(scalar, 'f', -1, 64)
			values[f] = scalar
		default:
			if _, have := cand.Dims[f]; !have && len(cand.seen) < maxCandidateDims {
				cand.seen[f] = map[string]bool{}
				cand.Dims[f] = []string{}
			}
		}
	}
	cand.observe(dims, values, state.MinuteOf(at))
}

func (c *Core) dropStalestCandidateLocked() {
	var stalest string
	var at time.Time
	for key, cand := range c.candidates {
		if stalest == "" || cand.Last.Before(at) {
			stalest, at = key, cand.Last
		}
	}
	delete(c.candidates, stalest)
}

// Candidates lists the signals that arrived without a definition, most recent first.
func (c *Core) Candidates() []Candidate {
	c.candMu.Lock()
	defer c.candMu.Unlock()
	out := make([]Candidate, 0, len(c.candidates))
	for _, cand := range c.candidates {
		copied := cand.Candidate
		copied.Dims = make(map[string][]string, len(cand.Dims))
		for dim, samples := range cand.Dims {
			copied.Dims[dim] = append([]string(nil), samples...)
		}
		copied.Values = make(map[string]Range, len(cand.Values))
		for field, r := range cand.Values {
			copied.Values[field] = r
		}
		copied.Minutes = make([]CandidateMinute, 0, len(cand.minutes))
		for _, m := range cand.minutes {
			sums := make(map[string]float64, len(m.Sums))
			for field, v := range m.Sums {
				sums[field] = v
			}
			copied.Minutes = append(copied.Minutes, CandidateMinute{Minute: m.Minute, Packets: m.Packets, Sums: sums})
		}
		sort.Slice(copied.Minutes, func(i, j int) bool { return copied.Minutes[i].Minute < copied.Minutes[j].Minute })
		out = append(out, copied)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Last.Equal(out[j].Last) {
			return out[i].Last.After(out[j].Last)
		}
		return candidateKey(out[i].Platform, out[i].Signal) < candidateKey(out[j].Platform, out[j].Signal)
	})
	return out
}

// DismissCandidate forgets an undefined signal: what was gathered about it
// and its packets that could not be placed. It is listed again if it arrives
// again.
func (c *Core) DismissCandidate(platform, sig string) {
	c.ForgetCandidate(platform, sig)
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, g := range c.quarantined {
		if g.Platform == platform && g.Signal == sig {
			delete(c.quarantined, key)
			delete(c.lastAlert, key)
		}
	}
}

// ForgetCandidate drops what was gathered about a signal once it is defined.
func (c *Core) ForgetCandidate(platform, sig string) {
	c.candMu.Lock()
	defer c.candMu.Unlock()
	delete(c.candidates, candidateKey(platform, sig))
}
