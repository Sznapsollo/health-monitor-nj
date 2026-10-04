package intake

import (
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/protocol"
)

func TestChunkTableIsBounded(t *testing.T) {
	now := time.Now()
	table := newChunkTable(time.Minute)

	if _, _, err := table.add(&protocol.Chunk{ID: "huge", Part: 1, Of: maxChunkParts + 1, Data: "x"}, now); err == nil {
		t.Fatal("an oversized part count was accepted")
	}
	if table.Pending() != 0 {
		t.Fatalf("pending = %d, want nothing kept for it", table.Pending())
	}

	for i := 0; i < maxPendingChunk+10; i++ {
		table.add(&protocol.Chunk{ID: "p" + strconv.Itoa(i), Part: 1, Of: 2, Data: "x"}, now)
	}
	if table.Pending() != maxPendingChunk {
		t.Fatalf("pending = %d, want the cap", table.Pending())
	}

	if full, done, err := table.add(&protocol.Chunk{ID: "p0", Part: 2, Of: 2, Data: "y"}, now); !done || err != nil || string(full) != "xy" {
		t.Fatalf("p0 = %q, %v, %v; want it reassembled", full, done, err)
	}
}

func TestChunkMemoryIsBounded(t *testing.T) {
	now := time.Now()
	table := newChunkTable(time.Minute)
	part := strings.Repeat("x", 60<<10)

	for i := 1; i*len(part) <= maxChunkBytes; i++ {
		if _, _, err := table.add(&protocol.Chunk{ID: "big", Part: i, Of: 100, Data: part}, now); err != nil {
			t.Fatalf("part %d refused below the per-payload limit: %v", i, err)
		}
	}
	if _, _, err := table.add(&protocol.Chunk{ID: "big", Part: 99, Of: 100, Data: part}, now); err == nil {
		t.Fatal("a payload past the per-payload limit was kept")
	}
	if table.Pending() != 0 || table.bytes != 0 {
		t.Fatalf("pending %d, bytes %d; want the oversized payload released", table.Pending(), table.bytes)
	}

	var refused bool
	for i := 0; i < 2*maxPendingBytes/len(part); i++ {
		if _, _, err := table.add(&protocol.Chunk{ID: "p" + strconv.Itoa(i), Part: 1, Of: 2, Data: part}, now); err != nil {
			refused = true
			break
		}
	}
	if !refused || table.bytes > maxPendingBytes {
		t.Fatalf("refused %v, bytes %d; want the total held under %d", refused, table.bytes, maxPendingBytes)
	}
}

func TestExpiredChunksFreeTheirMemory(t *testing.T) {
	now := time.Now()
	table := newChunkTable(time.Minute)
	table.add(&protocol.Chunk{ID: "lost", Part: 1, Of: 2, Data: "abc"}, now)

	table.add(&protocol.Chunk{ID: "next", Part: 1, Of: 2, Data: "d"}, now.Add(2*time.Minute))
	if table.Pending() != 1 || table.bytes != 1 {
		t.Fatalf("pending %d, bytes %d; want only the fresh payload left", table.Pending(), table.bytes)
	}
}

func TestPacketsOutsideTheClockWindowAreCounted(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	p := NewPipeline(PipelineOptions{Sink: benchSink{}, DropOlderThan: time.Hour, DropNewerThan: 2 * time.Minute})
	agg := NewAggregator(benchRegistry(), 100)
	packet := func(ts string) []byte {
		return []byte(`{"v":1,"t":"metric","platform":"p","signal":"requests","ts":"` + ts +
			`","dims":{"url":"/a"},"values":{"count":1,"ms":5}}`)
	}

	var c Counters
	for _, ts := range []string{
		"2026-09-24T12:01:00Z",
		"2026-09-24T12:05:00Z",
		"2026-09-24T10:00:00Z",
		"2026-09-24T11:30:00Z",
	} {
		p.Handle(packet(ts), netip.Addr{}, 1, agg, now, &c)
	}
	if c.Decoded != 2 || c.TooNew != 1 || c.TooOld != 1 {
		t.Errorf("decoded %d, tooNew %d, tooOld %d; want 2, 1, 1", c.Decoded, c.TooNew, c.TooOld)
	}
}

func TestARefusedChunkIsCounted(t *testing.T) {
	p := NewPipeline(PipelineOptions{Sink: benchSink{}})
	agg := NewAggregator(benchRegistry(), 100)
	var c Counters
	p.Handle([]byte(`{"v":1,"t":"chunk","id":"a","part":1,"of":5000,"data":"x"}`), netip.Addr{}, 1, agg, time.Now(), &c)
	p.Handle([]byte(`{"chunkId":"b","chunkPart":1,"chunkParts":5000,"data":"x"}`), netip.Addr{}, 1, agg, time.Now(), &c)
	if c.Chunks != 2 || c.ChunksDropped != 2 {
		t.Errorf("chunks %d, dropped %d; want both parts counted as refused", c.Chunks, c.ChunksDropped)
	}
}
