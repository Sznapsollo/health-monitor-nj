// Package intake reads datagrams from the UDP sockets, decodes them (v1
// directly, older formats through a platform adapter), folds metrics together
// per reader and hands the result to the core. It is the only part of the
// server whose cost scales with packet rate, so everything here is about
// doing less per packet.
package intake

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/config"
	"github.com/Sznapsollo/health-monitor-nj/internal/obs"
	"github.com/Sznapsollo/health-monitor-nj/internal/signal"
	"github.com/Sznapsollo/health-monitor-nj/internal/state"
)

// SamplesSink receives the aggregated metrics of one reader's flush.
type SamplesSink interface {
	Samples(samples []state.Sample)
}

// Options configure the intake.
type Options struct {
	Addr     string
	Config   config.Intake
	Log      *slog.Logger
	Metrics  *obs.Metrics
	Registry *signal.Registry
	Pipeline *Pipeline
	Sink     SamplesSink
	// FlushEvery bounds how long a reader holds aggregates before handing
	// them on; 250 ms by default.
	FlushEvery time.Duration
	// BatchSize is how many datagrams one syscall may return.
	BatchSize int
}

// Server owns the UDP listeners and the readers folding packets together.
type Server struct {
	o Options

	mu    sync.Mutex
	bound string
	ready chan struct{}

	counters Counters
	countMu  sync.Mutex

	flushes atomic.Int64
	samples atomic.Int64
	port    atomic.Int64
}

// New builds the intake.
func New(o Options) *Server {
	if o.FlushEvery <= 0 {
		o.FlushEvery = 250 * time.Millisecond
	}
	if o.BatchSize <= 0 {
		o.BatchSize = 64
	}
	return &Server{o: o, ready: make(chan struct{})}
}

// Ready is closed once the sockets are bound.
func (s *Server) Ready() <-chan struct{} { return s.ready }

// Addr is the address actually bound, which differs from the configured one
// when the port is 0.
func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bound
}

// Counters reports the loss accounting so far, including the datagrams the
// kernel dropped before the process saw them.
func (s *Server) Counters() Counters {
	s.countMu.Lock()
	out := s.counters
	s.countMu.Unlock()
	if port := int(s.port.Load()); port > 0 {
		if drops, ok := kernelDrops(port); ok {
			out.KernelDrops = drops
		}
	}
	return out
}

// Datagrams is how many datagrams have been read so far, without the cost of
// reading the kernel's drop counters that Counters pays.
func (s *Server) Datagrams() int64 {
	s.countMu.Lock()
	defer s.countMu.Unlock()
	return s.counters.Datagrams
}

// Run listens until ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	readers := s.o.Config.Readers
	if readers <= 0 {
		readers = min(runtime.NumCPU(), 8)
	}

	conns, err := s.listen(ctx, readers)
	if err != nil {
		return err
	}

	s.mu.Lock()
	s.bound = conns[0].LocalAddr().String()
	s.mu.Unlock()
	close(s.ready)

	port := conns[0].LocalAddr().(*net.UDPAddr).Port
	s.port.Store(int64(port))
	s.o.Log.Info("udp intake listening",
		"addr", s.Addr(), "readers", len(conns), "sockets", len(conns),
		"reuseport", reusePortSupported && len(conns) > 1)

	var wg sync.WaitGroup
	for i, conn := range conns {
		wg.Add(1)
		go func(i int, conn *net.UDPConn) {
			defer wg.Done()
			s.readLoop(ctx, conn, port)
		}(i, conn)
	}

	<-ctx.Done()
	for _, conn := range conns {
		_ = conn.Close()
	}
	wg.Wait()
	return nil
}

// listen opens one socket per reader where the platform allows it, so the
// kernel spreads datagrams across them; otherwise one socket is shared.
func (s *Server) listen(ctx context.Context, readers int) ([]*net.UDPConn, error) {
	if !reusePortSupported || readers == 1 {
		conn, err := listenUDP(ctx, s.o.Addr, false, s.o.Config.ReadBuffer)
		if err != nil {
			return nil, err
		}
		return []*net.UDPConn{conn}, nil
	}

	conns := make([]*net.UDPConn, 0, readers)
	for i := 0; i < readers; i++ {
		addr := s.o.Addr
		if i > 0 {
			// Every socket after the first must bind the port the first one
			// actually got, which matters when the configured port is 0.
			addr = conns[0].LocalAddr().String()
		}
		conn, err := listenUDP(ctx, addr, true, s.o.Config.ReadBuffer)
		if err != nil {
			if i == 0 {
				return nil, err
			}
			// One socket is enough to serve; log and carry on with fewer.
			s.o.Log.Warn("could not open an extra reader socket", "reader", i, "error", err)
			break
		}
		conns = append(conns, conn)
	}
	return conns, nil
}

func (s *Server) readLoop(ctx context.Context, conn *net.UDPConn, port int) {
	size := s.o.Config.MaxPacket
	if size <= 0 {
		size = 16 << 10
	}
	reader := newBatchReader(conn, s.o.BatchSize, size, len(s.o.Pipeline.resolver.BySender) > 0, s.o.Pipeline.tap)
	defer func() { _ = reader.Close() }()

	agg := NewAggregator(s.o.Registry, 5000)
	var counters Counters

	// One clock reading per batch, taken when it arrived: the read before it
	// can block for minutes on a quiet socket.
	var now time.Time
	handle := func(raw []byte, from netip.Addr, truncated bool) {
		if now.IsZero() {
			now = time.Now()
		}
		counters.Datagrams++
		counters.Bytes += int64(len(raw))
		counters.LargestDatagram = max(counters.LargestDatagram, int64(len(raw)))
		if truncated {
			counters.Truncated++
			return
		}
		s.o.Pipeline.Handle(raw, from, port, agg, now, &counters)
	}

	// While the aggregator is empty there is nothing to flush, so the read
	// blocks; as soon as it holds something, a deadline makes sure that
	// something reaches the core within FlushEvery of the last flush even if
	// no further packet ever arrives. It is set once per window, not per read.
	var deadline time.Time
	lastFlush := time.Now()

	for {
		if ctx.Err() != nil {
			s.flush(agg, &counters)
			return
		}

		want := time.Time{}
		if !agg.Empty() || counters.any() {
			want = lastFlush.Add(s.o.FlushEvery)
		}
		if !want.Equal(deadline) {
			_ = conn.SetReadDeadline(want)
			deadline = want
		}

		now = time.Time{}
		err := reader.Read(handle)
		if err != nil {
			switch {
			case errors.Is(err, os.ErrDeadlineExceeded):
				s.flush(agg, &counters)
				lastFlush = time.Now()
				continue
			case ctx.Err() != nil, errors.Is(err, net.ErrClosed):
				s.flush(agg, &counters)
				return
			}
			s.o.Metrics.ReadErrors.Inc()
			s.o.Log.Debug("udp read failed", "error", err)
			continue
		}
		if agg.Full() || time.Since(lastFlush) >= s.o.FlushEvery {
			s.flush(agg, &counters)
			lastFlush = time.Now()
		}
	}
}

func (s *Server) flush(agg *Aggregator, counters *Counters) {
	if packets := agg.TakePackets(); len(packets) > 0 {
		s.o.Pipeline.sink.Packets(packets)
	}
	samples := agg.Flush()
	if len(samples) > 0 && s.o.Sink != nil {
		s.o.Sink.Samples(samples)
		s.flushes.Add(1)
		s.samples.Add(int64(len(samples)))
	}
	if unknown := agg.TakeUnknown(); len(unknown) > 0 {
		s.o.Pipeline.sink.Quarantine(unknown, "unknown_signal")
	}

	s.countMu.Lock()
	s.counters.add(*counters)
	s.countMu.Unlock()
	s.publish(*counters)
	*counters = Counters{}
}

func (s *Server) publish(c Counters) {
	m := s.o.Metrics
	if c.Bytes > 0 {
		m.BytesReceived.Add(float64(c.Bytes))
	}
	if c.Decoded > 0 {
		m.PacketsReceived.WithLabelValues("v1").Add(float64(c.Decoded))
	}
	if c.Legacy > 0 {
		m.PacketsReceived.WithLabelValues("v0").Add(float64(c.Legacy))
	}
	for reason, n := range map[string]int64{
		"decode_error":   c.DecodeErrs,
		"unknown_signal": c.Quarantined,
		"too_old":        c.TooOld,
		"too_new":        c.TooNew,
		"chunk_dropped":  c.ChunksDropped,
		"truncated":      c.Truncated,
		"no_adapter":     c.NoAdapter,
	} {
		if n > 0 {
			m.PacketsRejected.WithLabelValues(reason).Add(float64(n))
		}
	}
}

func (c *Counters) add(b Counters) {
	c.Datagrams += b.Datagrams
	c.Bytes += b.Bytes
	c.Truncated += b.Truncated
	c.LargestDatagram = max(c.LargestDatagram, b.LargestDatagram)
	c.Decoded += b.Decoded
	c.Legacy += b.Legacy
	c.Chunks += b.Chunks
	c.Reassembled += b.Reassembled
	c.ChunksDropped += b.ChunksDropped
	c.DecodeErrs += b.DecodeErrs
	c.Quarantined += b.Quarantined
	c.TooOld += b.TooOld
	c.TooNew += b.TooNew
	c.NoAdapter += b.NoAdapter
}
