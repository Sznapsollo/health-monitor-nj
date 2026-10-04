package intake

import (
	"net/netip"
	"strconv"
	"testing"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/protocol"
	"github.com/Sznapsollo/health-monitor-nj/internal/signal"
)

type benchSink struct{}

func (benchSink) Packets([]protocol.Packet)            {}
func (benchSink) Quarantine([]protocol.Packet, string) {}

var (
	benchJavaTS = []byte(`{"v":1,"t":"metric","platform":"p","signal":"requests","source":"web-1","ts":"2026-09-18T10:11:12.123+0200","dims":{"url":"/api/x","user":"u","account":"42"},"values":{"count":1,"ms":340}}`)
	benchZuluTS = []byte(`{"v":1,"t":"metric","platform":"p","signal":"requests","source":"web-1","ts":"2026-09-18T10:11:12.123Z","dims":{"url":"/api/x","user":"u","account":"42"},"values":{"count":1,"ms":340}}`)
	benchNow    = time.Date(2026, 9, 18, 8, 11, 30, 0, time.UTC)
)

func benchRegistry() *signal.Registry {
	reg := signal.NewRegistry()
	_ = reg.Add(&signal.Definition{
		Platform: "p", Name: "requests", Kind: signal.KindTimeseries,
		Dims: []string{"url", "user", "account"}, Values: signal.Values{Count: "count", MS: "ms"},
	})
	return reg
}

func benchHandle(b *testing.B, raw []byte) {
	p := NewPipeline(PipelineOptions{Sink: benchSink{}})
	agg := NewAggregator(benchRegistry(), 5000)
	var c Counters
	b.ReportAllocs()
	for b.Loop() {
		p.Handle(raw, netip.Addr{}, 1, agg, benchNow, &c)
		if agg.Full() {
			agg.Flush()
		}
	}
}

func BenchmarkHandleJavaTimestamp(b *testing.B) { benchHandle(b, benchJavaTS) }

func BenchmarkHandleZuluTimestamp(b *testing.B) { benchHandle(b, benchZuluTS) }

func BenchmarkHandleParallel(b *testing.B) {
	p := NewPipeline(PipelineOptions{Sink: benchSink{}})
	reg := benchRegistry()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		agg := NewAggregator(reg, 5000)
		var c Counters
		for pb.Next() {
			p.Handle(benchJavaTS, netip.Addr{}, 1, agg, benchNow, &c)
			if agg.Full() {
				agg.Flush()
			}
		}
	})
}

func BenchmarkChunkPartWithFullTable(b *testing.B) {
	table := newChunkTable(time.Minute)
	now := time.Now()
	for i := 0; i < maxPendingChunk; i++ {
		table.add(&protocol.Chunk{ID: "p" + strconv.Itoa(i), Part: 1, Of: 2, Data: "x"}, now)
	}
	b.ReportAllocs()
	for b.Loop() {
		table.add(&protocol.Chunk{ID: "p1", Part: 1, Of: 2, Data: "x"}, now)
	}
}
