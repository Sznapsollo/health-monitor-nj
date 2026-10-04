package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/store"
)

func alertRow(id string, at time.Time, count int64, message string) store.AlertRow {
	return store.AlertRow{
		ID: id, Platform: "test", Level: "ERROR", Category: "jobs",
		Message: message, GroupKey: "jobs/import", Count: count,
		First: at, Last: at.Add(time.Duration(count) * time.Second),
	}
}

func TestAlertHistoryKeepsOneRowPerBurst(t *testing.T) {
	s := open(t)
	ctx := context.Background()

	// The same burst, written again as it grows: memory holds the running
	// total, so the row is replaced rather than added to.
	if err := s.WriteAlerts(ctx, []store.AlertRow{alertRow("a1", base, 1, "import failed")}); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteAlerts(ctx, []store.AlertRow{alertRow("a1", base, 12, "import failed again")}); err != nil {
		t.Fatal(err)
	}
	// A later burst of the same alert is its own row, which is what keeps
	// "it failed at 09:00 and again at 17:00" readable.
	later := base.Add(8 * time.Hour)
	if err := s.WriteAlerts(ctx, []store.AlertRow{alertRow("a2", later, 3, "import failed")}); err != nil {
		t.Fatal(err)
	}

	rows, err := s.SearchAlerts(ctx, store.AlertQuery{Platform: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %+v, want one per burst", rows)
	}
	// Newest burst first.
	if rows[0].ID != "a2" || rows[1].ID != "a1" {
		t.Errorf("order = %s, %s, want the newest burst first", rows[0].ID, rows[1].ID)
	}
	if rows[1].Count != 12 {
		t.Errorf("count = %d, want the running total, not a sum of writes", rows[1].Count)
	}
	if rows[1].Message != "import failed again" {
		t.Errorf("message = %q, want the latest occurrence", rows[1].Message)
	}
}

func TestAlertHistoryFilters(t *testing.T) {
	s := open(t)
	ctx := context.Background()

	warn := alertRow("w1", base, 1, "queue is filling")
	warn.Level = "WARN"
	warn.Category = "queues"
	silenced := alertRow("s1", base, 1, "known noise")
	silenced.Silenced = true
	silenced.SilenceReason = "muted by the platform's rules"

	err := s.WriteAlerts(ctx, []store.AlertRow{
		alertRow("e1", base, 2, "import failed"), warn, silenced,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Silenced alerts are recorded, but stay out of the way unless asked for.
	rows, err := s.SearchAlerts(ctx, store.AlertQuery{Platform: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want the silenced one left out by default", len(rows))
	}
	rows, err = s.SearchAlerts(ctx, store.AlertQuery{Platform: "test", Silenced: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want the silenced one included when asked", len(rows))
	}

	byLevel, err := s.SearchAlerts(ctx, store.AlertQuery{Platform: "test", Levels: []string{"WARN"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(byLevel) != 1 || byLevel[0].ID != "w1" {
		t.Errorf("rows = %+v, want only the warning", byLevel)
	}

	byText, err := s.SearchAlerts(ctx, store.AlertQuery{Platform: "test", Text: "import"})
	if err != nil {
		t.Fatal(err)
	}
	if len(byText) != 1 || byText[0].ID != "e1" {
		t.Errorf("rows = %+v, want the message match", byText)
	}

	// A window that ends before the burst began excludes it.
	none, err := s.SearchAlerts(ctx, store.AlertQuery{
		Platform: "test", From: base.Add(-48 * time.Hour), To: base.Add(-24 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Errorf("rows = %+v, want nothing outside the window", none)
	}
}

func TestAlertHistoryIsPurgedByDay(t *testing.T) {
	s := open(t)
	ctx := context.Background()

	old := base.AddDate(0, 0, -30)
	err := s.WriteAlerts(ctx, []store.AlertRow{
		alertRow("old", old, 1, "ancient"),
		alertRow("new", base, 1, "today"),
	})
	if err != nil {
		t.Fatal(err)
	}

	n, err := s.PurgeAlerts(ctx, base.AddDate(0, 0, -14).UTC().Format("20060102"))
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("purged = %d, want the one past the cutoff", n)
	}
	rows, err := s.SearchAlerts(ctx, store.AlertQuery{Platform: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != "new" {
		t.Errorf("rows = %+v, want only what is still in the window", rows)
	}

	days, err := s.AlertDays(ctx, "test")
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 1 || days[0] != base.UTC().Format("20060102") {
		t.Errorf("days = %v, want the day that is left", days)
	}
}
