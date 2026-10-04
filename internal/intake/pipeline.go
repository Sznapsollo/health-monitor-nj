package intake

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/netip"
	"sync"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/protocol"
)

// Adapter turns one legacy datagram into v1 packets. Each platform that still
// speaks an older format has one; the core never sees what they hide.
type Adapter interface {
	Adapt(raw []byte) ([]protocol.Packet, error)
}

// TypedAdapter also says which packet type the sender used, so it can be
// counted as sent rather than as what it became.
type TypedAdapter interface {
	AdaptTyped(raw []byte) ([]protocol.Packet, string, error)
}

// Sink receives the pipeline's output. Samples are the high-volume, already
// aggregated metrics; Packets are the low-volume envelopes (alerts, status,
// logs) that later phases consume; Quarantine holds what could not be placed.
type Sink interface {
	Packets(packets []protocol.Packet)
	Quarantine(packets []protocol.Packet, reason string)
}

// Resolver decides which platform a datagram belongs to, in this
// order: the envelope's own field, then the port it arrived on, then the
// sender's address, then "default".
type Resolver struct {
	// ByPort maps a listening port to a platform.
	ByPort map[int]string
	// BySender maps a sender address to a platform.
	BySender map[netip.Addr]string
	// Default is used when nothing else matches.
	Default string
}

// Resolve returns the platform for a packet.
func (r Resolver) Resolve(declared string, port int, from netip.Addr) string {
	if declared != "" {
		return declared
	}
	if p, ok := r.ByPort[port]; ok && p != "" {
		return p
	}
	if p, ok := r.BySender[from]; ok && p != "" {
		return p
	}
	if r.Default != "" {
		return r.Default
	}
	return "default"
}

// Counters are the loss-accounting numbers the errors panel and /api/metrics
// show. Every rejected datagram lands in exactly one of them.
type Counters struct {
	// Datagrams counts everything read off the sockets, whatever became of it.
	Datagrams int64 `json:"datagrams"`
	Bytes     int64 `json:"bytes"`
	// Truncated counts datagrams larger than max_packet_bytes, cut short by
	// the kernel and dropped.
	Truncated int64 `json:"truncated"`
	// LargestDatagram is the biggest datagram seen, in bytes.
	LargestDatagram int64 `json:"largestDatagram"`
	// Decoded counts envelopes routed, including those an adapter produced.
	Decoded int64 `json:"decoded"`
	// Legacy counts datagrams that arrived in an older format.
	Legacy      int64 `json:"legacy"`
	Chunks      int64 `json:"chunks"`
	Reassembled int64 `json:"reassembled"`
	// ChunksDropped counts parts refused by the reassembly limits.
	ChunksDropped int64 `json:"chunksDropped"`
	DecodeErrs    int64 `json:"decodeErrors"`
	Quarantined   int64 `json:"quarantined"`
	TooOld        int64 `json:"tooOld"`
	TooNew        int64 `json:"tooNew"`
	NoAdapter     int64 `json:"noAdapter"`
	// KernelDrops is what the kernel discarded before the process saw it,
	// read from /proc/net/udp on Linux. It is a running total, not a delta.
	KernelDrops int64 `json:"kernelDrops"`
}

// any reports whether anything has been counted since the last flush.
func (c Counters) any() bool {
	return c.Decoded+c.Legacy+c.Chunks+c.Reassembled+c.ChunksDropped+c.Truncated+c.DecodeErrs+
		c.Quarantined+c.TooOld+c.TooNew+c.NoAdapter > 0
}

// Pipeline turns datagrams into aggregated samples and envelopes. One instance
// is shared by every reader; the per-reader state lives in the Aggregator each
// reader owns.
type Pipeline struct {
	resolver Resolver
	adapters map[string]Adapter
	sink     Sink

	// dropOlderThan rejects packets whose timestamp is far in the past, the
	// old server's "start date anomaly" guard turned into a counted rule.
	dropOlderThan time.Duration
	dropNewerThan time.Duration

	chunks *chunkTable
	tap    *Tap
}

// PipelineOptions configures a pipeline.
type PipelineOptions struct {
	Resolver      Resolver
	Adapters      map[string]Adapter
	Sink          Sink
	DropOlderThan time.Duration
	DropNewerThan time.Duration
	ChunkTTL      time.Duration
	// Tap shows recent datagrams to whoever watches; nil shows none.
	Tap *Tap
}

// NewPipeline builds the decode and routing stage.
func NewPipeline(o PipelineOptions) *Pipeline {
	ttl := o.ChunkTTL
	if ttl <= 0 {
		ttl = time.Minute
	}
	return &Pipeline{
		resolver:      o.Resolver,
		adapters:      o.Adapters,
		sink:          o.Sink,
		dropOlderThan: o.DropOlderThan,
		dropNewerThan: o.DropNewerThan,
		chunks:        newChunkTable(ttl),
		tap:           o.Tap,
	}
}

// Handle processes one datagram. Metrics are folded into agg; everything else
// goes to the sink. The buffer may be reused as soon as Handle returns.
func (p *Pipeline) Handle(raw []byte, from netip.Addr, port int, agg *Aggregator, now time.Time, c *Counters) {
	packets, err := protocol.DecodeBatch(raw)
	if err != nil {
		if errors.Is(err, protocol.ErrLegacy) {
			c.Legacy++
			p.handleLegacy(raw, from, port, agg, now, c)
			return
		}
		c.DecodeErrs++
		p.sink.Quarantine(nil, "decode_error")
		p.tap.record(now, raw, from, port, "", "", "", "decode_error")
		return
	}
	for _, packet := range packets {
		if packet.Chunk == nil {
			platform, kind := p.resolver.Resolve(packet.Platform, port, from), string(packet.T)+":"+packet.Signal()
			agg.Count(platform, packet.Source, kind, now)
			p.tap.record(now, raw, from, port, platform, packet.Source, kind, "")
		}
		p.route(packet, from, port, agg, now, c)
	}
}

func (p *Pipeline) route(packet protocol.Packet, from netip.Addr, port int, agg *Aggregator, now time.Time, c *Counters) {
	packet.Platform = p.resolver.Resolve(packet.Platform, port, from)
	if packet.Metric != nil {
		packet.Metric.Platform = packet.Platform
	}

	if packet.Chunk != nil {
		c.Chunks++
		full, done, err := p.chunks.add(packet.Chunk, now)
		if err != nil {
			c.ChunksDropped++
			return
		}
		if !done {
			return
		}
		c.Reassembled++
		p.Handle(full, from, port, agg, now, c)
		return
	}

	if p.tooOld(packet.TS.Time, now) {
		c.TooOld++
		return
	}
	if p.tooNew(packet.TS.Time, now) {
		c.TooNew++
		return
	}
	c.Decoded++

	if packet.Metric != nil {
		if !agg.Add(packet, now) {
			c.Quarantined++
			p.sink.Quarantine(agg.TakeUnknown(), "unknown_signal")
		}
		return
	}
	agg.AddPacket(packet)
}

func (p *Pipeline) tooOld(ts time.Time, now time.Time) bool {
	if p.dropOlderThan <= 0 || ts.IsZero() {
		return false
	}
	return now.Sub(ts) > p.dropOlderThan
}

func (p *Pipeline) tooNew(ts time.Time, now time.Time) bool {
	if p.dropNewerThan <= 0 || ts.IsZero() {
		return false
	}
	return ts.Sub(now) > p.dropNewerThan
}

var legacyChunkMark = []byte(`"chunkId"`)

// legacyChunk is the old server's chunked envelope; legacy senders split big
// messages with it.
type legacyChunk struct {
	ChunkID    string `json:"chunkId"`
	ChunkPart  int    `json:"chunkPart"`
	ChunkParts int    `json:"chunkParts"`
	Data       string `json:"data"`
}

func (p *Pipeline) handleLegacy(raw []byte, from netip.Addr, port int, agg *Aggregator, now time.Time, c *Counters) {
	platform := p.resolver.Resolve("", port, from)

	if bytes.Contains(raw, legacyChunkMark) {
		var lc legacyChunk
		if err := json.Unmarshal(raw, &lc); err == nil && lc.ChunkID != "" {
			c.Chunks++
			full, done, err := p.chunks.add(&protocol.Chunk{
				ID: lc.ChunkID, Part: lc.ChunkPart, Of: lc.ChunkParts, Data: lc.Data,
			}, now)
			if err != nil {
				c.ChunksDropped++
				return
			}
			if !done {
				return
			}
			c.Reassembled++
			p.Handle(full, from, port, agg, now, c)
			return
		}
	}

	adapter, ok := p.adapters[platform]
	if !ok {
		c.NoAdapter++
		p.sink.Quarantine(nil, "no_adapter")
		p.tap.record(now, raw, from, port, platform, "", "", "no_adapter")
		return
	}
	var (
		packets []protocol.Packet
		kind    string
		err     error
	)
	if typed, ok := adapter.(TypedAdapter); ok {
		packets, kind, err = typed.AdaptTyped(raw)
	} else {
		packets, err = adapter.Adapt(raw)
	}
	if err != nil {
		c.DecodeErrs++
		p.sink.Quarantine(nil, "adapter_error")
		p.tap.record(now, raw, from, port, platform, "", "", "adapter_error")
		return
	}
	if len(packets) > 0 {
		if kind == "" {
			kind = string(packets[0].T) + ":" + packets[0].Signal()
		}
		agg.Count(platform, packets[0].Source, kind, now)
		p.tap.record(now, raw, from, port, platform, packets[0].Source, kind, "")
	}
	for _, packet := range packets {
		if packet.Platform == "" {
			packet.Platform = platform
		}
		p.route(packet, from, port, agg, now, c)
	}
}

const (
	maxChunkParts   = 1024
	maxPendingChunk = 4096
	// maxChunkBytes caps one reassembled payload; maxPendingBytes caps every
	// partial payload together, so lost parts cannot pile up memory.
	maxChunkBytes   = 1 << 20
	maxPendingBytes = 16 << 20
	chunkSweepEvery = time.Second
)

var errChunkRejected = errors.New("chunk rejected")

// chunkTable reassembles datagrams that were split by the sender.
type chunkTable struct {
	ttl time.Duration

	mu        sync.Mutex
	pending   map[string]*pendingChunk
	bytes     int
	nextSweep time.Time
}

type pendingChunk struct {
	parts    []string
	have     int
	bytes    int
	deadline time.Time
}

func newChunkTable(ttl time.Duration) *chunkTable {
	return &chunkTable{ttl: ttl, pending: make(map[string]*pendingChunk)}
}

// add stores one part and returns the whole payload once every part arrived.
// An error means the part, and with it the payload it belongs to, was refused.
func (t *chunkTable) add(c *protocol.Chunk, now time.Time) ([]byte, bool, error) {
	if c.ID == "" || c.Of <= 0 || c.Of > maxChunkParts || c.Part <= 0 || c.Part > c.Of || len(c.Data) > maxChunkBytes {
		return nil, false, errChunkRejected
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if !now.Before(t.nextSweep) {
		t.expireLocked(now)
		t.nextSweep = now.Add(chunkSweepEvery)
	}

	p, ok := t.pending[c.ID]
	if ok && len(p.parts) != c.Of {
		// A sender changed its mind about the part count; start again.
		t.dropLocked(c.ID, p)
		ok = false
	}
	if !ok {
		if len(t.pending) >= maxPendingChunk {
			return nil, false, errChunkRejected
		}
		p = &pendingChunk{parts: make([]string, c.Of), deadline: now.Add(t.ttl)}
		t.pending[c.ID] = p
	}

	grow := len(c.Data) - len(p.parts[c.Part-1])
	if p.bytes+grow > maxChunkBytes || t.bytes+grow > maxPendingBytes {
		t.dropLocked(c.ID, p)
		return nil, false, errChunkRejected
	}
	if p.parts[c.Part-1] == "" && c.Data != "" {
		p.have++
	}
	p.parts[c.Part-1] = c.Data
	p.bytes += grow
	t.bytes += grow
	if p.have < len(p.parts) {
		return nil, false, nil
	}
	t.dropLocked(c.ID, p)
	full := make([]byte, 0, p.bytes)
	for _, part := range p.parts {
		full = append(full, part...)
	}
	return full, true, nil
}

func (t *chunkTable) dropLocked(id string, p *pendingChunk) {
	delete(t.pending, id)
	t.bytes -= p.bytes
}

func (t *chunkTable) expireLocked(now time.Time) {
	for id, p := range t.pending {
		if now.After(p.deadline) {
			t.dropLocked(id, p)
		}
	}
}

// Pending is how many partial payloads are waiting, for the metrics endpoint.
func (t *chunkTable) Pending() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.pending)
}
