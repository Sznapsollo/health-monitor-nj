package gauge_test

import (
	"testing"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/gauge"
)

var base = time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)

type clock struct{ t time.Time }

func (c *clock) now() time.Time      { return c.t }
func (c *clock) add(d time.Duration) { c.t = c.t.Add(d) }

func TestLatestValueWins(t *testing.T) {
	c := &clock{t: base}
	s := gauge.NewStore(c.now, time.Minute)

	s.Observe("test", "queueDepth", "jobs-1", []gauge.Point{{Label: "mail", Value: 12}}, c.now())
	c.add(10 * time.Second)
	s.Observe("test", "queueDepth", "jobs-1", []gauge.Point{{Label: "mail", Value: 3}}, c.now())

	v := s.View("test", "queueDepth")
	if len(v.Points) != 1 || v.Points[0].Value != 3 {
		t.Fatalf("points = %+v, want only the latest reading", v.Points)
	}
}

func TestSourcesAreAddedTogether(t *testing.T) {
	c := &clock{t: base}
	s := gauge.NewStore(c.now, time.Minute)

	s.Observe("test", "vpnUsers", "gw-1", []gauge.Point{{Label: "office", Value: 4}}, c.now())
	s.Observe("test", "vpnUsers", "gw-2", []gauge.Point{
		{Label: "office", Value: 3}, {Label: "remote", Value: 2, Warn: true},
	}, c.now())

	v := s.View("test", "vpnUsers")
	byLabel := map[string]gauge.Point{}
	for _, p := range v.Points {
		byLabel[p.Label] = p
	}
	if byLabel["office"].Value != 7 {
		t.Errorf("office = %v, want both gateways counted", byLabel["office"].Value)
	}
	if !byLabel["remote"].Warn {
		t.Error("a warning from one source should show")
	}
	if len(v.Sources) != 2 {
		t.Errorf("sources = %v", v.Sources)
	}
}

func TestASilentSourceDropsOff(t *testing.T) {
	c := &clock{t: base}
	s := gauge.NewStore(c.now, time.Minute)
	s.Observe("test", "vpnUsers", "gw-1", []gauge.Point{{Label: "office", Value: 4}}, c.now())
	s.Observe("test", "vpnUsers", "gw-2", []gauge.Point{{Label: "office", Value: 3}}, c.now())

	// gw-2 keeps reporting; gw-1 stops.
	c.add(90 * time.Second)
	s.Observe("test", "vpnUsers", "gw-2", []gauge.Point{{Label: "office", Value: 3}}, c.now())

	v := s.View("test", "vpnUsers")
	if len(v.Sources) != 1 || v.Sources[0] != "gw-2" {
		t.Fatalf("sources = %v, want only the one still reporting", v.Sources)
	}
	if v.Points[0].Value != 3 {
		t.Errorf("value = %v, want the silent gateway left out", v.Points[0].Value)
	}
}

func TestSweepForgetsLongGoneSources(t *testing.T) {
	c := &clock{t: base}
	s := gauge.NewStore(c.now, time.Minute)
	s.Observe("test", "queueDepth", "jobs-1", []gauge.Point{{Label: "mail", Value: 1}}, c.now())

	c.add(5 * time.Minute)
	if dropped := s.Sweep(); dropped != 0 {
		t.Errorf("dropped %d too early: a brief gap should not forget a source", dropped)
	}
	c.add(2 * time.Hour)
	if dropped := s.Sweep(); dropped != 1 {
		t.Errorf("dropped = %d, want the long-gone source forgotten", dropped)
	}
	if len(s.Signals("test")) != 0 {
		t.Error("the signal is still listed after its only source was forgotten")
	}
}

func TestSignalsListsWhatIsReporting(t *testing.T) {
	c := &clock{t: base}
	s := gauge.NewStore(c.now, time.Minute)
	s.Observe("test", "queueDepth", "jobs-1", nil, c.now())
	s.Observe("test", "vpnUsers", "gw-1", nil, c.now())
	s.Observe("other", "somethingElse", "x", nil, c.now())

	got := s.Signals("test")
	if len(got) != 2 || got[0] != "queueDepth" || got[1] != "vpnUsers" {
		t.Fatalf("signals = %v, want this platform's, sorted", got)
	}
}

func TestEachSignalMayHaveItsOwnTTL(t *testing.T) {
	c := &clock{t: base}
	s := gauge.NewStore(c.now, time.Minute)
	s.SetTTLFor(func(_, signal string) time.Duration {
		if signal == "slowReporter" {
			return 10 * time.Minute
		}
		return 0 // the default
	})
	s.Observe("test", "slowReporter", "a", []gauge.Point{{Label: "x", Value: 1}}, c.now())
	s.Observe("test", "queueDepth", "a", []gauge.Point{{Label: "x", Value: 1}}, c.now())

	c.add(5 * time.Minute)
	if got := s.Signals("test"); len(got) != 1 || got[0] != "slowReporter" {
		t.Fatalf("signals = %v, want only the one whose own TTL has not passed", got)
	}
	if v := s.View("test", "queueDepth"); len(v.Points) != 0 {
		t.Errorf("queueDepth = %+v, want it gone after the default minute", v.Points)
	}
	if v := s.View("test", "slowReporter"); len(v.Points) != 1 {
		t.Errorf("slowReporter = %+v, want it still shown", v.Points)
	}
}
