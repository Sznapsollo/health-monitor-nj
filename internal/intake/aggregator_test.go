package intake_test

import (
	"testing"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/intake"
	"github.com/Sznapsollo/health-monitor-nj/internal/protocol"
	"github.com/Sznapsollo/health-monitor-nj/internal/signal"
	"github.com/Sznapsollo/health-monitor-nj/internal/state"
)

var now = time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)

func testRegistry(t *testing.T) *signal.Registry {
	t.Helper()
	reg := signal.NewRegistry()
	err := reg.Add(&signal.Definition{
		Platform: "p", Name: "requests", Kind: signal.KindTimeseries,
		Dims:   []string{"url", "user"},
		Values: signal.Values{Count: "count", MS: "ms"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func metric(sig string, dims map[string]string, values map[string]float64) protocol.Packet {
	h := protocol.Header{V: 1, T: protocol.TypeMetric, Platform: "p", TS: protocol.NewTimestamp(now)}
	return protocol.Packet{Header: h, Metric: &protocol.Metric{Header: h, Signal: sig, Dims: dims, Values: values}}
}

func TestAggregatorFoldsIdenticalDimensions(t *testing.T) {
	a := intake.NewAggregator(testRegistry(t), 100)
	for i := 0; i < 5; i++ {
		if !a.Add(metric("requests", map[string]string{"url": "/a"}, map[string]float64{"count": 1, "ms": 100}), now) {
			t.Fatal("packet was not accepted")
		}
	}
	a.Add(metric("requests", map[string]string{"url": "/b"}, map[string]float64{"count": 1, "ms": 20}), now)

	if a.Len() != 2 {
		t.Fatalf("entries = %d, want one per distinct url", a.Len())
	}
	samples := a.Flush()
	if len(samples) != 2 {
		t.Fatalf("samples = %d", len(samples))
	}
	if a.Len() != 0 {
		t.Error("the aggregator kept its entries after a flush")
	}

	var busy state.Sample
	for _, s := range samples {
		if s.Dims["url"] == "/a" {
			busy = s
		}
	}
	if busy.Agg.Count != 5 {
		t.Errorf("count = %d, want the five folded together", busy.Agg.Count)
	}
	if busy.Agg.AvgMS() != 100 {
		t.Errorf("avg = %v", busy.Agg.AvgMS())
	}
	if busy.Minute != state.MinuteOf(now) {
		t.Errorf("minute = %d", busy.Minute)
	}
}

func TestAggregatorSeparatesMinutes(t *testing.T) {
	a := intake.NewAggregator(testRegistry(t), 100)
	later := now.Add(time.Minute)
	p := metric("requests", map[string]string{"url": "/a"}, map[string]float64{"count": 1, "ms": 10})
	a.Add(p, now)
	p2 := p
	p2.Header.TS = protocol.NewTimestamp(later)
	p2.Metric.Header.TS = protocol.NewTimestamp(later)
	a.Add(p2, later)
	if a.Len() != 2 {
		t.Fatalf("entries = %d, want one per minute", a.Len())
	}
}

func TestAggregatorIgnoresUndeclaredDimensions(t *testing.T) {
	a := intake.NewAggregator(testRegistry(t), 100)
	a.Add(metric("requests", map[string]string{"url": "/a", "sessionId": "s1"}, map[string]float64{"count": 1}), now)
	a.Add(metric("requests", map[string]string{"url": "/a", "sessionId": "s2"}, map[string]float64{"count": 1}), now)

	if a.Len() != 1 {
		t.Fatalf("entries = %d: an undeclared field multiplied the cardinality", a.Len())
	}
	s := a.Flush()[0]
	if _, ok := s.Dims["sessionId"]; ok {
		t.Errorf("dims = %+v, want only declared dimensions", s.Dims)
	}
	if s.Agg.Count != 2 {
		t.Errorf("count = %d", s.Agg.Count)
	}
}

func TestAggregatorPreAggregatedPacket(t *testing.T) {
	a := intake.NewAggregator(testRegistry(t), 100)
	// One datagram standing for ten requests totalling 1000 ms.
	a.Add(metric("requests", map[string]string{"url": "/a"},
		map[string]float64{"count": 10, "ms": 1000, "samples": 10, "minMs": 20, "maxMs": 400}), now)
	s := a.Flush()[0]
	if s.Agg.Count != 10 || s.Agg.Samples != 10 || s.Agg.AvgMS() != 100 {
		t.Fatalf("agg = %+v, avg = %v", s.Agg, s.Agg.AvgMS())
	}
	if s.Agg.MinMS != 20 || s.Agg.MaxMS != 400 {
		t.Errorf("min/max = %v/%v, want the sender's own", s.Agg.MinMS, s.Agg.MaxMS)
	}
}

func TestAggregatorUnknownSignalIsQuarantined(t *testing.T) {
	a := intake.NewAggregator(testRegistry(t), 100)
	if a.Add(metric("mystery", map[string]string{"url": "/a"}, map[string]float64{"count": 1}), now) {
		t.Fatal("an unknown signal was accepted")
	}
	got := a.TakeUnknown()
	if len(got) != 1 || got[0].Metric.Signal != "mystery" {
		t.Fatalf("quarantined = %+v", got)
	}
	if a.TakeUnknown() != nil {
		t.Error("the quarantine was not cleared")
	}
}

func TestAggregatorAutoRegisters(t *testing.T) {
	reg := testRegistry(t)
	reg.SetAutoRegister("p", true)
	a := intake.NewAggregator(reg, 100)
	if !a.Add(metric("invented", map[string]string{"queue": "mail"}, map[string]float64{"count": 3}), now) {
		t.Fatal("auto-registration did not happen")
	}
	s := a.Flush()[0]
	if s.Signal != "invented" || s.Dims["queue"] != "mail" || s.Agg.Count != 3 {
		t.Fatalf("sample = %+v", s)
	}
}

func TestAggregatorFullTriggersFlush(t *testing.T) {
	a := intake.NewAggregator(testRegistry(t), 2)
	a.Add(metric("requests", map[string]string{"url": "/a"}, map[string]float64{"count": 1}), now)
	if a.Full() {
		t.Fatal("full too early")
	}
	a.Add(metric("requests", map[string]string{"url": "/b"}, map[string]float64{"count": 1}), now)
	if !a.Full() {
		t.Fatal("the aggregator should be asking to be flushed")
	}
}

func BenchmarkAggregatorAdd(b *testing.B) {
	reg := signal.NewRegistry()
	_ = reg.Add(&signal.Definition{
		Platform: "p", Name: "requests", Kind: signal.KindTimeseries,
		Dims: []string{"url", "user"}, Values: signal.Values{Count: "count", MS: "ms"},
	})
	a := intake.NewAggregator(reg, 100000)
	p := metric("requests", map[string]string{"url": "/api/x", "user": "u1"},
		map[string]float64{"count": 1, "ms": 42})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.Add(p, now)
	}
}
