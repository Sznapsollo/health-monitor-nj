package alert_test

import (
	"testing"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/alert"
	"github.com/Sznapsollo/health-monitor-nj/internal/protocol"
)

var base = time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)

type clock struct{ t time.Time }

func (c *clock) now() time.Time      { return c.t }
func (c *clock) add(d time.Duration) { c.t = c.t.Add(d) }

func newStore(c *clock) *alert.Store {
	return alert.NewStore(alert.Options{Now: c.now, GroupWindow: time.Minute})
}

func warn(message string) alert.Alert {
	return alert.Alert{Platform: "test", Level: protocol.LevelWarn, Category: "latency", Message: message}
}

func TestRepeatsCollapseWithinTheWindow(t *testing.T) {
	c := &clock{t: base}
	s := newStore(c)

	first, isNew := s.Add(warn("slow"))
	if !isNew || first.Count != 1 {
		t.Fatalf("first = %+v, new = %v", first, isNew)
	}

	c.add(10 * time.Second)
	second, isNew := s.Add(warn("slow"))
	if isNew {
		t.Error("a repeat inside the window should not be new noise")
	}
	if second.Count != 2 {
		t.Errorf("count = %d, want the repeat folded in", second.Count)
	}
	if len(s.List("test", alert.Filter{})) != 1 {
		t.Error("the repeat became its own row")
	}

	// Past the window it is news again.
	c.add(2 * time.Minute)
	third, isNew := s.Add(warn("slow"))
	if !isNew || third.Count != 1 {
		t.Errorf("after the window: %+v, new = %v", third, isNew)
	}
	if got := len(s.List("test", alert.Filter{})); got != 2 {
		t.Errorf("rows = %d, want two", got)
	}
}

func TestSenderGroupKeyWins(t *testing.T) {
	c := &clock{t: base}
	s := newStore(c)

	a := warn("gateway timeout on attempt 1")
	a.GroupKey = "gateway"
	b := warn("gateway timeout on attempt 2")
	b.GroupKey = "gateway"

	s.Add(a)
	got, isNew := s.Add(b)
	if isNew {
		t.Error("two messages sharing a group key should collapse")
	}
	if got.Count != 2 {
		t.Errorf("count = %d", got.Count)
	}
}

func TestDifferentPlatformsDoNotCollapse(t *testing.T) {
	c := &clock{t: base}
	s := newStore(c)
	a := warn("same text")
	b := warn("same text")
	b.Platform = "other"

	s.Add(a)
	if _, isNew := s.Add(b); !isNew {
		t.Fatal("alerts from two platforms were folded together")
	}
	if len(s.List("test", alert.Filter{})) != 1 || len(s.List("other", alert.Filter{})) != 1 {
		t.Error("the platforms do not each have their own row")
	}
}

func TestFilters(t *testing.T) {
	c := &clock{t: base}
	s := newStore(c)

	slow := warn("slow request on /api/export")
	slow.Data = map[string]any{"ms": 2500.0}
	s.Add(slow)

	c.add(2 * time.Minute)
	boom := alert.Alert{
		Platform: "test", Level: protocol.LevelError, Category: "job",
		Message: "job failed", Data: map[string]any{"ms": 12.0},
	}
	s.Add(boom)

	tests := []struct {
		name   string
		filter alert.Filter
		want   int
	}{
		{name: "everything", want: 2},
		{name: "by level", filter: alert.Filter{Levels: []protocol.Level{protocol.LevelError}}, want: 1},
		{name: "by category", filter: alert.Filter{Categories: []string{"LATENCY"}}, want: 1},
		{name: "by text", filter: alert.Filter{Text: "export"}, want: 1},
		{name: "text matches any term", filter: alert.Filter{Text: "nothing, failed"}, want: 1},
		{name: "by latency", filter: alert.Filter{MinLatencyMS: 1000}, want: 1},
		{name: "since", filter: alert.Filter{Since: base.Add(time.Minute)}, want: 1},
		{name: "limit", filter: alert.Filter{Limit: 1}, want: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := len(s.List("test", tc.filter)); got != tc.want {
				t.Errorf("got %d alerts, want %d", got, tc.want)
			}
		})
	}
}

func TestNewestFirst(t *testing.T) {
	c := &clock{t: base}
	s := newStore(c)
	s.Add(warn("first"))
	c.add(2 * time.Minute)
	s.Add(warn("second"))

	list := s.List("test", alert.Filter{})
	if len(list) != 2 || list[0].Message != "second" {
		t.Fatalf("list = %+v, want the newest first", list)
	}
}

func TestSilencedAreHiddenUnlessAskedFor(t *testing.T) {
	c := &clock{t: base}
	s := newStore(c)
	quiet := warn("known noise")
	quiet.Silenced = true
	quiet.SilenceReason = "maintenance"
	s.Add(quiet)

	if got := len(s.List("test", alert.Filter{})); got != 0 {
		t.Errorf("silenced alerts = %d in the normal list, want none", got)
	}
	got := s.List("test", alert.Filter{IncludeSilenced: true})
	if len(got) != 1 || got[0].SilenceReason != "maintenance" {
		t.Fatalf("list = %+v, want the silenced alert with its reason", got)
	}
	// And they do not colour the badges.
	if counts := s.Counts("test"); counts[protocol.LevelWarn] != 0 {
		t.Errorf("counts = %+v, want silenced alerts left out", counts)
	}
}

func TestOldAlertsExpire(t *testing.T) {
	c := &clock{t: base}
	s := alert.NewStore(alert.Options{Now: c.now, Keep: time.Hour})
	s.Add(warn("old"))

	c.add(2 * time.Hour)
	s.Add(warn("new"))

	list := s.List("test", alert.Filter{})
	if len(list) != 1 || list[0].Message != "new" {
		t.Fatalf("list = %+v, want only the recent alert", list)
	}
}

func TestCategoriesAndCounts(t *testing.T) {
	c := &clock{t: base}
	s := newStore(c)
	s.Add(warn("a"))
	c.add(2 * time.Minute)
	s.Add(alert.Alert{Platform: "test", Level: protocol.LevelError, Category: "job", Message: "b"})

	cats := s.Categories("test")
	if len(cats) != 2 || cats[0] != "job" || cats[1] != "latency" {
		t.Errorf("categories = %v, want them sorted", cats)
	}
	counts := s.Counts("test")
	if counts[protocol.LevelWarn] != 1 || counts[protocol.LevelError] != 1 {
		t.Errorf("counts = %+v", counts)
	}
	if st := s.Stats(); st.Raised != 2 || st.Live != 2 {
		t.Errorf("stats = %+v", st)
	}
}

func TestCapIsEnforced(t *testing.T) {
	c := &clock{t: base}
	s := alert.NewStore(alert.Options{Now: c.now, Max: 3, GroupWindow: time.Nanosecond})
	for i := 0; i < 10; i++ {
		c.add(time.Second)
		s.Add(warn(string(rune('a' + i))))
	}
	list := s.List("test", alert.Filter{})
	if len(list) != 3 {
		t.Fatalf("rows = %d, want the cap of 3", len(list))
	}
	if list[0].Message != "j" {
		t.Errorf("newest = %q, want the last one added", list[0].Message)
	}
}

func TestAGroupShowsTheWorstItHasSeen(t *testing.T) {
	c := &clock{t: base}
	s := newStore(c)

	first := warn("/api/export took 1900 ms")
	first.GroupKey = "latency//api/export"
	first.Data = map[string]any{"ms": 1900.0}
	s.Add(first)

	c.add(5 * time.Second)
	worse := warn("/api/export took 2700 ms")
	worse.GroupKey = "latency//api/export"
	worse.Data = map[string]any{"ms": 2700.0}
	got, isNew := s.Add(worse)

	if isNew {
		t.Fatal("the repeat should have been grouped")
	}
	if got.Count != 2 {
		t.Errorf("count = %d", got.Count)
	}
	// Reporting the first number would understate the problem.
	if got.Message != "/api/export took 2700 ms" {
		t.Errorf("message = %q, want the most recent occurrence", got.Message)
	}
	if got.Data["ms"] != 2700.0 {
		t.Errorf("data = %+v, want the latest", got.Data)
	}
	if !got.First.Equal(base) {
		t.Errorf("first = %v, want when the group started", got.First)
	}
}

func TestAGroupKeepsTheWorstLevel(t *testing.T) {
	c := &clock{t: base}
	s := newStore(c)

	mild := warn("gateway wobbling")
	mild.GroupKey = "gateway"
	s.Add(mild)

	c.add(5 * time.Second)
	bad := mild
	bad.Level = protocol.LevelError
	bad.Message = "gateway down"
	got, _ := s.Add(bad)
	if got.Level != protocol.LevelError {
		t.Errorf("level = %q, want the escalation to show", got.Level)
	}

	// And it does not drop back down on the next mild repeat.
	c.add(5 * time.Second)
	got, _ = s.Add(mild)
	if got.Level != protocol.LevelError {
		t.Errorf("level = %q, want the worst level kept", got.Level)
	}
}

func TestRestoreKeepsIDsAndFoldsRepeats(t *testing.T) {
	c := &clock{t: base}
	s := newStore(c)

	old := warn("slow")
	old.ID, old.Count, old.First, old.Last = "stale", 3, base.Add(-48*time.Hour), base.Add(-48*time.Hour)
	recent := warn("slow")
	recent.ID, recent.Count, recent.First, recent.Last = "abc", 4, base.Add(-time.Hour), base.Add(-30*time.Second)

	if n := s.Restore([]alert.Alert{recent, old}); n != 1 {
		t.Fatalf("restored = %d, want only the one inside the keep window", n)
	}
	got, isNew := s.Add(warn("slow"))
	if isNew || got.ID != "abc" || got.Count != 5 {
		t.Errorf("after restore: %+v, new = %v", got, isNew)
	}
	if rows := s.List("test", alert.Filter{}); len(rows) != 1 {
		t.Errorf("rows = %d, want one", len(rows))
	}
}

func TestExpiringAnOldAlertKeepsTheNewerGroupWithItsKey(t *testing.T) {
	c := &clock{t: base}
	s := alert.NewStore(alert.Options{Now: c.now, Keep: time.Hour, GroupWindow: 45 * time.Minute})
	s.Add(warn("slow"))

	c.add(50 * time.Minute)
	if _, isNew := s.Add(warn("slow")); !isNew {
		t.Fatal("fixture: past the group window a repeat should start a new alert")
	}

	// The first alert expires on this add; the second is still in its window.
	c.add(20 * time.Minute)
	if _, isNew := s.Add(warn("slow")); isNew {
		t.Error("the repeat started another alert: expiring the old one took the newer one's key with it")
	}
}

func TestGroupingGoesByArrivalNotTheSendersClock(t *testing.T) {
	c := &clock{t: base}
	s := newStore(c)

	behind := warn("slow")
	behind.Last = base.Add(-2 * time.Hour)
	if _, isNew := s.Add(behind); !isNew {
		t.Fatal("the first alert should be new")
	}
	c.add(10 * time.Second)
	behind.Last = base.Add(-2*time.Hour + 10*time.Second)
	got, isNew := s.Add(behind)
	if isNew || got.Count != 2 {
		t.Fatalf("a repeat from a lagging clock was not folded: %+v, new = %v", got, isNew)
	}
	if !got.Last.Equal(behind.Last) {
		t.Errorf("last = %v, want the sender's time %v", got.Last, behind.Last)
	}

	ahead := warn("fast")
	ahead.Last = base.Add(2 * time.Hour)
	s.Add(ahead)
	c.add(2 * time.Minute)
	if _, isNew := s.Add(ahead); !isNew {
		t.Error("a clock running ahead kept a group open past the window")
	}
}

func TestExpiryGoesByArrival(t *testing.T) {
	c := &clock{t: base}
	s := alert.NewStore(alert.Options{Now: c.now, Keep: time.Hour})

	old := warn("stale stamp")
	old.Last = base.Add(-3 * time.Hour)
	s.Add(old)
	c.add(2 * time.Second)
	s.Add(warn("other"))
	if got := len(s.List("test", alert.Filter{})); got != 2 {
		t.Fatalf("rows = %d, want the lagging alert kept until an hour after it arrived", got)
	}
}
