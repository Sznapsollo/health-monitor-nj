// Package core is where the decoded world lands: it owns the hot state, keeps
// what could not be placed, and tells subscribers which minutes changed.
package core

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/alert"
	"github.com/Sznapsollo/health-monitor-nj/internal/protocol"
	"github.com/Sznapsollo/health-monitor-nj/internal/signal"
	"github.com/Sznapsollo/health-monitor-nj/internal/state"
)

// quarantineKeep bounds the distinct kinds of unplaceable packet kept; the
// least recently seen goes first.
const quarantineKeep = 500

// alertDebounce keeps a flood of unknown packets from becoming a flood of
// internal alerts, like the old server's decode-error debounce.
const alertDebounce = 5 * time.Minute

// Core ties the pieces together.
type Core struct {
	store   *state.Store
	reg     *signal.Registry
	log     *slog.Logger
	now     func() time.Time
	durable Durable

	mu          sync.Mutex
	subscribers []func([]state.Dirty)
	quarantined map[string]*QuarantineGroup
	lastAlert   map[string]time.Time

	// pending holds envelopes the later phases own (logs).
	pending map[protocol.Type]int64

	candMu     sync.Mutex
	candidates map[string]*candidate

	alertingFields

	disk diskState

	// checks is when the hourly rollover started and how often it runs;
	// guarded by mu.
	checks rolloverClock

	// reminded is the last reminder step per quiet entity; guarded by mu.
	reminded         map[string]int
	pendingReminders []reminder
}

// QuarantineGroup counts the packets that could not be placed for one reason,
// platform, signal and packet type.
type QuarantineGroup struct {
	Key        string    `json:"key"`
	Reason     string    `json:"reason"`
	Platform   string    `json:"platform,omitempty"`
	Signal     string    `json:"signal,omitempty"`
	Type       string    `json:"type,omitempty"`
	Count      int64     `json:"count"`
	First      time.Time `json:"first"`
	Last       time.Time `json:"last"`
	LastSource string    `json:"lastSource,omitempty"`
}

// New builds the core around a store.
func New(store *state.Store, reg *signal.Registry, log *slog.Logger, now func() time.Time) *Core {
	if now == nil {
		now = time.Now
	}
	return &Core{
		store:       store,
		reg:         reg,
		log:         log,
		now:         now,
		quarantined: make(map[string]*QuarantineGroup),
		lastAlert:   make(map[string]time.Time),
		pending:     make(map[protocol.Type]int64),
	}
}

// Store is the hot state, for the API and the hub.
func (c *Core) Store() *state.Store { return c.store }

// Subscribe registers a listener for changed minutes. The hub uses it to know
// what to broadcast.
func (c *Core) Subscribe(fn func([]state.Dirty)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.subscribers = append(c.subscribers, fn)
}

// SetDurable attaches the batched writer, so everything folded into the hot
// state is also on its way to disk.
func (c *Core) SetDurable(d Durable) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.durable = d
}

// Samples folds one reader's flush into the hot state. It implements
// intake.SamplesSink.
func (c *Core) Samples(samples []state.Sample) {
	if len(samples) == 0 {
		return
	}
	c.mu.Lock()
	durable := c.durable
	c.mu.Unlock()
	if durable != nil {
		durable.Samples(samples)
	}

	c.evaluateSamples(samples)

	dirty, unknown := c.store.Apply(samples)
	for _, s := range unknown {
		c.quarantineOne(QuarantineGroup{
			Reason: "unknown_signal", Platform: s.Platform, Signal: s.Signal, Type: string(protocol.TypeMetric),
		}, c.now())
	}
	if len(dirty) == 0 {
		return
	}
	c.mu.Lock()
	subs := make([]func([]state.Dirty), len(c.subscribers))
	copy(subs, c.subscribers)
	c.mu.Unlock()
	for _, fn := range subs {
		fn(dirty)
	}
}

// Packets receives the envelopes that are not metrics: alerts and heartbeats
// are acted on here, logs are counted until Phase 3 gives them a home.
func (c *Core) Packets(packets []protocol.Packet) {
	var logs []protocol.Packet
	counts := make(map[protocol.Type]int64, 4)
	for _, p := range packets {
		switch {
		case p.Alert != nil:
			c.Raise(alert.Alert{
				Platform: p.Platform,
				Signal:   p.Alert.Signal,
				Level:    p.Alert.Level,
				Category: p.Alert.Category,
				Message:  p.Alert.Message,
				GroupKey: p.Alert.GroupKey,
				Source:   p.Source,
				Data:     p.Alert.Data,
				Last:     p.TS.Time,
			})
		case p.Status != nil:
			c.observeStatus(p)
		case p.Log != nil:
			logs = append(logs, p)
		case p.Gauge != nil:
			c.observeGauge(p)
		}
		counts[p.T]++
	}
	c.writeLogs(logs)

	c.mu.Lock()
	for t, n := range counts {
		c.pending[t] += n
	}
	c.mu.Unlock()
}

// Quarantine keeps what could not be placed, and raises one internal alert per
// reason per debounce window.
func (c *Core) Quarantine(packets []protocol.Packet, reason string) {
	now := c.now()
	if len(packets) == 0 {
		c.quarantineOne(QuarantineGroup{Reason: reason}, now)
		return
	}
	for _, p := range packets {
		c.quarantineOne(QuarantineGroup{
			Reason: reason, Platform: p.Platform, Signal: p.Signal(), Type: string(p.T), LastSource: p.Source,
		}, now)
		c.observeCandidate(p, now)
	}
}

func (c *Core) quarantineOne(g QuarantineGroup, at time.Time) {
	key := g.Reason + "/" + g.Platform + "/" + g.Signal + "/" + g.Type
	c.mu.Lock()
	have, ok := c.quarantined[key]
	if !ok {
		if len(c.quarantined) >= quarantineKeep {
			c.forgetOldestLocked()
		}
		g.Key, g.First = key, at
		have = &g
		c.quarantined[key] = have
	}
	have.Count++
	have.Last = at
	if g.LastSource != "" {
		have.LastSource = g.LastSource
	}
	shout := at.Sub(c.lastAlert[key]) > alertDebounce
	if shout {
		c.lastAlert[key] = at
	}
	c.mu.Unlock()

	if shout {
		c.log.Warn("packet could not be placed",
			"reason", g.Reason, "platform", g.Platform, "signal", g.Signal, "type", g.Type)
	}
}

func (c *Core) forgetOldestLocked() {
	var oldest *QuarantineGroup
	for _, g := range c.quarantined {
		if oldest == nil || g.Last.Before(oldest.Last) {
			oldest = g
		}
	}
	if oldest != nil {
		delete(c.quarantined, oldest.Key)
		delete(c.lastAlert, oldest.Key)
	}
}

// QuarantineSnapshot returns the kinds of packet that could not be placed,
// most recently seen first.
func (c *Core) QuarantineSnapshot() []QuarantineGroup {
	c.mu.Lock()
	out := make([]QuarantineGroup, 0, len(c.quarantined))
	for _, g := range c.quarantined {
		out = append(out, *g)
	}
	c.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Last.Equal(out[j].Last) {
			return out[i].Last.After(out[j].Last)
		}
		return out[i].Key < out[j].Key
	})
	return out
}

// PendingCounts reports the envelopes received but not yet charted.
func (c *Core) PendingCounts() map[protocol.Type]int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[protocol.Type]int64, len(c.pending))
	for k, v := range c.pending {
		out[k] = v
	}
	return out
}

// Maintain applies the retention tiers until ctx is cancelled.
func (c *Core) Maintain(ctx context.Context, every time.Duration) {
	if every <= 0 {
		every = 30 * time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.store.Maintain()
			c.mu.Lock()
			gauges := c.alerting.Gauges
			c.mu.Unlock()
			if gauges != nil {
				gauges.Sweep()
			}
		}
	}
}
