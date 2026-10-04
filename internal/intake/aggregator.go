package intake

import (
	"strconv"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/protocol"
	"github.com/Sznapsollo/health-monitor-nj/internal/signal"
	"github.com/Sznapsollo/health-monitor-nj/internal/state"
)

// Aggregator folds metric packets together inside one reader goroutine, so the
// core merges a few hundred aggregates a second instead of touching shared
// state once per packet. It is not safe for concurrent use: each
// reader owns one.
type Aggregator struct {
	reg     *signal.Registry
	entries map[string]*entry
	// maxKeys forces an early flush rather than letting one reader's map grow
	// without bound during a burst.
	maxKeys int
	// Unknown collects packets whose signal the registry does not know, so the
	// caller can quarantine them instead of dropping them silently.
	unknown []protocol.Packet
	// packets holds the envelopes that are not metrics until the reader's
	// next flush, so the core takes its lock once per batch, not per packet.
	packets []protocol.Packet
	// key is reused for every packet, so a packet for an existing entry costs
	// no allocation.
	key []byte
	// packetsDef caches each platform's built-in packets signal.
	packetsDef map[string]*signal.Definition
	countDims  map[string]string
}

type entry struct {
	sample state.Sample
}

// NewAggregator builds an aggregator for one reader.
func NewAggregator(reg *signal.Registry, maxKeys int) *Aggregator {
	if maxKeys <= 0 {
		maxKeys = 5000
	}
	return &Aggregator{reg: reg, entries: make(map[string]*entry, maxKeys), maxKeys: maxKeys}
}

// Len is how many distinct (signal, minute, dimensions) the aggregator holds.
func (a *Aggregator) Len() int { return len(a.entries) }

// Full reports whether the aggregator should be flushed now.
func (a *Aggregator) Full() bool {
	return len(a.entries) >= a.maxKeys || len(a.packets) >= a.maxKeys
}

// Empty reports whether there is nothing to flush.
func (a *Aggregator) Empty() bool { return len(a.entries) == 0 && len(a.packets) == 0 }

// AddPacket keeps an envelope that is not a metric for the next flush.
func (a *Aggregator) AddPacket(p protocol.Packet) { a.packets = append(a.packets, p) }

// TakePackets returns the envelopes kept since the last flush.
func (a *Aggregator) TakePackets() []protocol.Packet {
	out := a.packets
	a.packets = nil
	return out
}

// Add folds one metric packet in. It reports false when the packet is not a
// metric the registry knows; the packet is then kept for quarantine.
func (a *Aggregator) Add(p protocol.Packet, now time.Time) bool {
	if p.Metric == nil {
		return false
	}
	def, ok := a.reg.Lookup(p.Platform, p.Metric.Signal)
	if !ok {
		def, ok = a.reg.LookupOrRegister(p.Platform, p.Metric.Signal, signal.KindTimeseries, dimNames(p.Metric.Dims))
	}
	if !ok || def.Kind != signal.KindTimeseries {
		a.unknown = append(a.unknown, p)
		return false
	}

	ts := p.TS.Time
	if ts.IsZero() {
		ts = now
	}
	minute := state.MinuteOf(ts)

	a.key = appendKey(a.key[:0], def, p.Platform, minute, p.Metric.Dims)
	e, ok := a.entries[string(a.key)]
	if !ok {
		e = &entry{sample: state.Sample{
			Platform: p.Platform,
			Signal:   def.Name,
			Minute:   minute,
			Dims:     keptDims(def, p.Metric.Dims),
		}}
		a.entries[string(a.key)] = e
	}
	e.sample.Agg.Merge(aggOf(def, p.Metric.Values))
	return true
}

// Count adds one received packet to the platform's built-in packets signal,
// by sender and type, in the minute it arrived.
func (a *Aggregator) Count(platform, sender, kind string, now time.Time) {
	if platform == "" {
		return
	}
	def, ok := a.packetsDef[platform]
	if !ok {
		if a.packetsDef == nil {
			a.packetsDef = map[string]*signal.Definition{}
		}
		def = a.reg.Packets(platform)
		if def == nil {
			return
		}
		a.packetsDef[platform] = def
	}
	if sender == "" {
		sender = "unknown"
	}
	if a.countDims == nil {
		a.countDims = make(map[string]string, 2)
	}
	a.countDims["sender"], a.countDims["type"] = sender, kind
	minute := state.MinuteOf(now)
	a.key = appendKey(a.key[:0], def, platform, minute, a.countDims)
	e, ok := a.entries[string(a.key)]
	if !ok {
		e = &entry{sample: state.Sample{
			Platform: platform, Signal: def.Name, Minute: minute, Dims: keptDims(def, a.countDims),
		}}
		a.entries[string(a.key)] = e
	}
	e.sample.Agg.Add(1, 0, false)
}

// Flush returns everything gathered and resets the aggregator.
func (a *Aggregator) Flush() []state.Sample {
	if len(a.entries) == 0 {
		return nil
	}
	out := make([]state.Sample, 0, len(a.entries))
	for _, e := range a.entries {
		out = append(out, e.sample)
	}
	clear(a.entries)
	return out
}

// TakeUnknown returns the packets whose signal was not known, and forgets them.
func (a *Aggregator) TakeUnknown() []protocol.Packet {
	if len(a.unknown) == 0 {
		return nil
	}
	out := a.unknown
	a.unknown = nil
	return out
}

// aggOf reads the packet's values through the signal's own field names, so a
// sender that calls its duration something else still feeds the chart.
func aggOf(def *signal.Definition, values map[string]float64) state.Agg {
	var a state.Agg
	count := int64(1)
	if name := def.Values.Count; name != "" {
		if v, ok := values[name]; ok {
			count = int64(v)
		}
	}
	if count <= 0 {
		count = 1
	}

	ms, hasMS := 0.0, false
	if name := def.Values.MS; name != "" {
		ms, hasMS = values[name]
	}
	if !hasMS {
		a.Add(count, 0, false)
		return a
	}

	// A sender that pre-aggregated says so; otherwise the packet is one
	// measurement.
	samples := int64(1)
	if v, ok := values["samples"]; ok && v > 0 {
		samples = int64(v)
	}
	minMS, maxMS := ms, ms
	if samples > 1 {
		minMS, maxMS = ms/float64(samples), ms/float64(samples)
	}
	if v, ok := values["minMs"]; ok {
		minMS = v
	}
	if v, ok := values["maxMs"]; ok {
		maxMS = v
	}
	a.AddSummed(count, ms, samples, minMS, maxMS, true)
	return a
}

// appendKey builds the map key a sample is stored under. Only declared
// dimensions take part, so an extra field a sender adds cannot multiply the
// cardinality of the cube.
func appendKey(b []byte, def *signal.Definition, platform string, minute int64, dims map[string]string) []byte {
	b = append(b, platform...)
	b = append(b, 0)
	b = append(b, def.Name...)
	b = append(b, 0)
	b = strconv.AppendInt(b, minute, 10)
	for _, name := range def.Dims {
		b = append(b, 0)
		b = append(b, dims[name]...)
	}
	return b
}

// keptDims is the dimension set a new entry is stored with.
func keptDims(def *signal.Definition, dims map[string]string) map[string]string {
	kept := make(map[string]string, len(def.Dims))
	for _, name := range def.Dims {
		if v := dims[name]; v != "" {
			kept[name] = v
		}
	}
	return kept
}

func dimNames(dims map[string]string) []string {
	out := make([]string, 0, len(dims))
	for k := range dims {
		out = append(out, k)
	}
	return out
}
