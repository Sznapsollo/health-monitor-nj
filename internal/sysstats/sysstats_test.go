package sysstats

import (
	"testing"
	"time"
)

func TestCPUIsTheShareOfOneCoreBetweenSamples(t *testing.T) {
	at := time.Unix(1000, 0)
	used := time.Duration(0)
	s := New(10 * time.Second)
	s.now = func() time.Time { return at }
	s.cpu = func() time.Duration { return used }

	s.sample()
	for _, busy := range []time.Duration{5 * time.Second, 15 * time.Second, 1 * time.Second} {
		at = at.Add(10 * time.Second)
		used += busy
		s.sample()
	}
	got := s.Snapshot(true)
	want := []float64{0, 50, 150, 10}
	for i, h := range got.History {
		if h.CPUPercent != want[i] {
			t.Errorf("sample %d: %v%%, want %v%%", i, h.CPUPercent, want[i])
		}
	}
	if got.CPUPercent != 10 || got.CPUPercentMin1 != 52.5 {
		t.Errorf("now %v%%, last minute %v%%", got.CPUPercent, got.CPUPercentMin1)
	}
	if got.RSSBytes <= 0 || got.Goroutines <= 0 || got.Cores <= 0 {
		t.Errorf("snapshot = %+v", got)
	}
}

func TestKeepsAnHour(t *testing.T) {
	at := time.Unix(1000, 0)
	s := New(time.Minute)
	s.now = func() time.Time { return at }
	for range 100 {
		s.sample()
		at = at.Add(time.Minute)
	}
	if n := len(s.Snapshot(true).History); n != 60 {
		t.Errorf("kept %d samples, want 60", n)
	}
	if s.Snapshot(false).History != nil {
		t.Error("history sent when not asked for")
	}
}

func TestPacketRateIsTheDeltaPerSecond(t *testing.T) {
	at := time.Unix(1000, 0)
	var total int64 = 500
	s := New(10 * time.Second)
	s.now = func() time.Time { return at }
	s.CountPackets(func() int64 { return total })

	s.sample()
	at = at.Add(10 * time.Second)
	total += 300
	s.sample()
	if got := s.Snapshot(false).PacketsPerSec; got != 30 {
		t.Errorf("now %v/s, want 30", got)
	}
}

func TestPacketsLastMinuteCoversOnlyCompleteMinutes(t *testing.T) {
	at := time.Unix(1000, 0)
	var total int64
	s := New(10 * time.Second)
	s.now = func() time.Time { return at }
	s.CountPackets(func() int64 { return total })

	step := func() {
		at = at.Add(10 * time.Second)
		total += 10
		s.sample()
	}
	s.sample()
	for at.Unix() < 1070 {
		step()
		if got := s.Snapshot(false).PacketsLastMinute; got != nil {
			t.Fatalf("at %d reported %d before a full minute", at.Unix(), *got)
		}
	}
	step()
	if got := s.Snapshot(false).PacketsLastMinute; got == nil || *got != 60 {
		t.Errorf("last minute = %v, want 60", got)
	}
}

func TestLogsLastMinuteIsCountedApartFromPackets(t *testing.T) {
	at := time.Unix(1000, 0)
	var packets, logs int64
	s := New(10 * time.Second)
	s.now = func() time.Time { return at }
	s.CountPackets(func() int64 { return packets })
	s.CountLogs(func() int64 { return logs })

	s.sample()
	for at.Unix() < 1080 {
		at = at.Add(10 * time.Second)
		packets += 10
		logs += 2
		s.sample()
	}
	got := s.Snapshot(false)
	if got.LogsLastMinute == nil || *got.LogsLastMinute != 12 || got.PacketsLastMinute == nil || *got.PacketsLastMinute != 60 {
		t.Errorf("logs %v, packets %v, want 12 and 60", got.LogsLastMinute, got.PacketsLastMinute)
	}
}
