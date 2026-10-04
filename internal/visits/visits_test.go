package visits_test

import (
	"context"
	"testing"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/store"
	"github.com/Sznapsollo/health-monitor-nj/internal/visits"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func open(t *testing.T) (*store.Store, *clock) {
	t.Helper()
	db, err := store.Open(store.Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, &clock{t: time.Date(2026, 9, 26, 8, 0, 0, 0, time.UTC)}
}

func TestATabIsOneVisitThroughItsReconnects(t *testing.T) {
	db, c := open(t)
	ctx := context.Background()
	tr, err := visits.Open(ctx, db.DB(), c.now)
	if err != nil {
		t.Fatal(err)
	}
	who := visits.Who{Tab: "tab-1", Login: "anna", Kind: "user", IP: "10.0.0.7", UserAgent: "Chrome", Platform: "example"}

	id := tr.Connect(ctx, who)
	c.t = c.t.Add(time.Hour)
	tr.Disconnect(ctx, id, 100, []string{"requests"})
	c.t = c.t.Add(2 * time.Minute)
	if again := tr.Connect(ctx, who); again != id {
		t.Fatalf("a reconnect after 2 min started visit %s, want %s continued", again, id)
	}
	c.t = c.t.Add(time.Hour)
	tr.Disconnect(ctx, id, 50, []string{"checkout"})

	c.t = c.t.Add(10 * time.Minute)
	later := tr.Connect(ctx, who)
	if later == id {
		t.Fatal("a tab back after 10 min continued the old visit")
	}

	list, err := tr.Day(ctx, "20260926")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("visits = %+v, want two", list)
	}
	first := list[1]
	if first.ID != id || first.Reconnects != 1 || first.Sent != 150 || len(first.Signals) != 2 ||
		first.Ended.Sub(first.Started) != 2*time.Hour+2*time.Minute || first.Open {
		t.Errorf("first visit = %+v", first)
	}
	if !list[0].Open || list[0].Login != "anna" || list[0].IP != "10.0.0.7" {
		t.Errorf("current visit = %+v", list[0])
	}
}

func TestAVisitSurvivesARestartOfTheMonitor(t *testing.T) {
	db, c := open(t)
	ctx := context.Background()
	tr, err := visits.Open(ctx, db.DB(), c.now)
	if err != nil {
		t.Fatal(err)
	}
	who := visits.Who{Tab: "wall", Login: "hall", Kind: "display"}
	id := tr.Connect(ctx, who)
	c.t = c.t.Add(30 * time.Second)
	tr.Seen(ctx)

	c.t = c.t.Add(time.Minute)
	restarted, err := visits.Open(ctx, db.DB(), c.now)
	if err != nil {
		t.Fatal(err)
	}
	if again := restarted.Connect(ctx, who); again != id {
		t.Errorf("after a restart the screen started %s, want %s continued", again, id)
	}
	days, err := restarted.Days(ctx)
	if err != nil || len(days) != 1 || days[0] != "20260926" {
		t.Errorf("days = %v, %v", days, err)
	}

	c.t = c.t.Add(40 * 24 * time.Hour)
	restarted.Disconnect(ctx, id, 0, nil)
	if n, err := restarted.Purge(ctx, c.t.Add(-time.Hour)); err != nil || n != 0 {
		t.Errorf("purged %d, %v; a visit that just ended is kept", n, err)
	}
	if n, _ := restarted.Purge(ctx, c.t.Add(time.Hour)); n != 1 {
		t.Errorf("purged %d, want the ended visit gone", n)
	}
}
