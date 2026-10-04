package protocol_test

import (
	"errors"
	"testing"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/protocol"
)

func TestDecode(t *testing.T) {
	tests := []struct {
		name  string
		input string
		check func(*testing.T, protocol.Packet)
	}{
		{
			name:  "metric",
			input: `{"v":1,"t":"metric","signal":"requests","source":"web-1","ts":"2026-09-18T10:11:12.123+0200","dims":{"url":"/api/x","user":"u"},"values":{"count":1,"ms":340}}`,
			check: func(t *testing.T, p protocol.Packet) {
				if p.Metric == nil {
					t.Fatal("metric payload is nil")
				}
				if p.Metric.Signal != "requests" {
					t.Errorf("signal = %q", p.Metric.Signal)
				}
				if got := p.Metric.Values["ms"]; got != 340 {
					t.Errorf("ms = %v", got)
				}
				if got := p.Metric.Dims["url"]; got != "/api/x" {
					t.Errorf("url = %q", got)
				}
				want := time.Date(2026, 9, 18, 10, 11, 12, 123e6, time.FixedZone("", 2*3600))
				if !p.TS.Time.Equal(want) {
					t.Errorf("ts = %v, want %v", p.TS.Time, want)
				}
			},
		},
		{
			name:  "gauge",
			input: `{"v":1,"t":"gauge","signal":"queueDepth","source":"jobs-1","points":[{"label":"mail","value":12,"warn":false}]}`,
			check: func(t *testing.T, p protocol.Packet) {
				if len(p.Gauge.Points) != 1 || p.Gauge.Points[0].Label != "mail" {
					t.Fatalf("points = %+v", p.Gauge.Points)
				}
			},
		},
		{
			name:  "status",
			input: `{"v":1,"t":"status","signal":"servers","key":"web-1","payload":{"branch":"master","build":"abc123"}}`,
			check: func(t *testing.T, p protocol.Packet) {
				if p.Status.Key != "web-1" || p.Status.Payload["branch"] != "master" {
					t.Fatalf("status = %+v", p.Status)
				}
			},
		},
		{
			name:  "alert",
			input: `{"v":1,"t":"alert","level":"WARN","category":"latency","message":"slow","groupKey":"g1","data":{"ms":900}}`,
			check: func(t *testing.T, p protocol.Packet) {
				if p.Alert.Level != protocol.LevelWarn || p.Alert.GroupKey != "g1" {
					t.Fatalf("alert = %+v", p.Alert)
				}
			},
		},
		{
			name:  "log",
			input: `{"v":1,"t":"log","signal":"sendLogs","key":"2026-09-18","data":{"to":"a@b.c"}}`,
			check: func(t *testing.T, p protocol.Packet) {
				if p.Log.Signal != "sendLogs" {
					t.Fatalf("log = %+v", p.Log)
				}
			},
		},
		{
			name:  "chunk",
			input: `{"v":1,"t":"chunk","id":"abc","part":1,"of":3,"data":"{\"x\":"}`,
			check: func(t *testing.T, p protocol.Packet) {
				if p.Chunk.Part != 1 || p.Chunk.Of != 3 {
					t.Fatalf("chunk = %+v", p.Chunk)
				}
			},
		},
		{
			name:  "missing ts",
			input: `{"v":1,"t":"metric","signal":"requests"}`,
			check: func(t *testing.T, p protocol.Packet) {
				if !p.TS.Time.IsZero() {
					t.Errorf("ts = %v, want zero", p.TS.Time)
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p, err := protocol.Decode([]byte(tc.input))
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			tc.check(t, p)
		})
	}
}

func TestDecodeErrors(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr error
	}{
		{name: "legacy v0", input: `{"type":"request","start":"2026-09-18T10:11:12.123+0200"}`, wantErr: protocol.ErrLegacy},
		{name: "unknown type", input: `{"v":1,"t":"nonsense"}`},
		{name: "unsupported version", input: `{"v":7,"t":"metric","signal":"x"}`},
		{name: "not json", input: `not json at all`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := protocol.Decode([]byte(tc.input))
			if err == nil {
				t.Fatal("want an error, got none")
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestDecodeBatch(t *testing.T) {
	in := `[{"v":1,"t":"metric","signal":"requests","values":{"count":1}},
	        {"v":1,"t":"metric","signal":"jobs","values":{"count":2}}]`
	got, err := protocol.DecodeBatch([]byte(in))
	if err != nil {
		t.Fatalf("decode batch: %v", err)
	}
	if len(got) != 2 || got[0].Signal() != "requests" || got[1].Signal() != "jobs" {
		t.Fatalf("batch = %+v", got)
	}

	single, err := protocol.DecodeBatch([]byte(`{"v":1,"t":"alert","level":"INFO","message":"hi"}`))
	if err != nil || len(single) != 1 {
		t.Fatalf("single = %+v, err = %v", single, err)
	}
}

func TestTimestampLayouts(t *testing.T) {
	tests := []struct {
		in   string
		want time.Time
	}{
		{`"2026-09-18T10:11:12.123+0200"`, time.Date(2026, 9, 18, 10, 11, 12, 123e6, time.FixedZone("", 7200))},
		{`"2026-09-18T10:11:12.123+02:00"`, time.Date(2026, 9, 18, 10, 11, 12, 123e6, time.FixedZone("", 7200))},
		{`"2026-09-18T10:11:12Z"`, time.Date(2026, 9, 18, 10, 11, 12, 0, time.UTC)},
		{`"2026-09-18T10:11:12.123Z"`, time.Date(2026, 9, 18, 10, 11, 12, 123e6, time.UTC)},
		{`"2026-09-18T10:11:12.123456789+02:00"`, time.Date(2026, 9, 18, 10, 11, 12, 123456789, time.FixedZone("", 7200))},
		{`"2026-09-18T10:11:12+0200"`, time.Date(2026, 9, 18, 10, 11, 12, 0, time.FixedZone("", 7200))},
		{`"2026-09-18T10:11:12.123-0500"`, time.Date(2026, 9, 18, 10, 11, 12, 123e6, time.FixedZone("", -18000))},
		{`"2026-09-18T10:11:12.123"`, time.Date(2026, 9, 18, 10, 11, 12, 123e6, time.Local)},
		{`"2026-09-18T10:11:12"`, time.Date(2026, 9, 18, 10, 11, 12, 0, time.Local)},
		{`1758182472123`, time.UnixMilli(1758182472123)},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			var ts protocol.Timestamp
			if err := ts.UnmarshalJSON([]byte(tc.in)); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if !ts.Time.Equal(tc.want) {
				t.Fatalf("got %v, want %v", ts.Time, tc.want)
			}
		})
	}

	var ts protocol.Timestamp
	if err := ts.UnmarshalJSON([]byte(`"18 September 2026"`)); err == nil {
		t.Fatal("want an error for an unrecognised layout")
	}
}

var benchPacket = []byte(`{"v":1,"t":"metric","signal":"requests","source":"web-1","ts":"2026-09-18T10:11:12.123+0200","dims":{"url":"/api/x","user":"u","account":"42"},"values":{"count":1,"ms":340}}`)

func BenchmarkDecodeMetric(b *testing.B) {
	b.SetBytes(int64(len(benchPacket)))
	for i := 0; i < b.N; i++ {
		if _, err := protocol.Decode(benchPacket); err != nil {
			b.Fatal(err)
		}
	}
}

func TestTimestampWithoutZoneIsLocal(t *testing.T) {
	saved := time.Local
	time.Local = time.FixedZone("UTC+2", 7200)
	t.Cleanup(func() { time.Local = saved })

	got, err := protocol.ParseTime("2026-09-18T10:11:12.123")
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 9, 18, 8, 11, 12, 123e6, time.UTC); !got.Equal(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	got, err = protocol.ParseTime("2026-09-18T10:11:12Z")
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 9, 18, 10, 11, 12, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
