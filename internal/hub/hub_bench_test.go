package hub_test

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/hub"
	"github.com/Sznapsollo/health-monitor-nj/internal/signal"
	"github.com/Sznapsollo/health-monitor-nj/internal/state"
)

// marshaller encodes each message the way the WebSocket sender does.
type marshaller struct{ bytes int }

func (m *marshaller) Send(msg hub.ServerMessage) error {
	out, err := json.Marshal(msg)
	m.bytes += len(out)
	return err
}

func (m *marshaller) SendRaw(msg []byte) error {
	m.bytes += len(msg)
	return nil
}

func (m *marshaller) Close() {}

var benchCharts = []state.Criteria{
	{ID: "total", Signal: "requests"},
	{ID: "byUrl", Signal: "requests", Group: "url"},
	{ID: "byUrlUser", Signal: "requests", Group: "url", Sub: "user"},
	{ID: "byUser", Signal: "requests", Group: "user"},
	{ID: "byUserUrl", Signal: "requests", Group: "user", Sub: "url"},
}

// benchFlush measures one flush of two dirty minutes to viewers sessions,
// each showing the five charts above over 120 minutes of 100 urls × 10 users.
func benchFlush(b *testing.B, viewers int) {
	now := base.Add(200 * time.Minute)
	reg := signal.NewRegistry()
	_ = reg.Add(&signal.Definition{
		Platform: "test", Name: "requests", Kind: signal.KindTimeseries,
		Dims:      []string{"url", "user"},
		Values:    signal.Values{Count: "count", MS: "ms"},
		Retention: signal.Retention{HotDetailMinutes: 120, HotTotalsMinutes: 1440},
	})
	clock := func() time.Time { return now }
	store := state.NewStore(reg, clock)
	h := hub.New(store, reg, clock, time.Second)
	for range viewers {
		s := h.Add(&marshaller{})
		if err := h.Hello(s, hub.ClientMessage{T: hub.TypeHello, Platform: "test", Batch: true, Criteria: benchCharts}); err != nil {
			b.Fatal(err)
		}
	}

	var last []state.Dirty
	for m := 119; m >= 0; m-- {
		minute := state.MinuteOf(now.Add(-time.Duration(m) * time.Minute))
		samples := make([]state.Sample, 0, 1000)
		for u := 0; u < 100; u++ {
			for us := 0; us < 10; us++ {
				var a state.Agg
				a.Add(1, float64(u+us), true)
				samples = append(samples, state.Sample{
					Platform: "test", Signal: "requests", Minute: minute, Agg: a,
					Dims: map[string]string{"url": fmt.Sprintf("/u/%d", u), "user": fmt.Sprintf("user%d", us)},
				})
			}
		}
		dirty, _ := store.Apply(samples)
		if m < 2 {
			last = append(last, dirty...)
		}
	}
	h.Flush()

	b.ReportAllocs()
	for b.Loop() {
		h.Dirty(last)
		h.Flush()
	}
}

func BenchmarkFlush1Viewer(b *testing.B)   { benchFlush(b, 1) }
func BenchmarkFlush10Viewers(b *testing.B) { benchFlush(b, 10) }
func BenchmarkFlush20Viewers(b *testing.B) { benchFlush(b, 20) }
