package health_test

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/health"
	_ "modernc.org/sqlite"
)

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestCounterIsNewAgainWhenItGrows(t *testing.T) {
	db := openDB(t)
	book, err := health.Open(db, nil)
	if err != nil {
		t.Fatal(err)
	}
	drops := []health.Issue{{Key: health.CounterPrefix + "writer.errors", Source: health.SourceCounter, Count: 4}}
	if err := book.Mark(drops, []string{drops[0].Key}, true, "anna"); err != nil {
		t.Fatal(err)
	}
	if got := book.Annotate(drops); !got[0].Known || got[0].New != 0 {
		t.Errorf("marked = %+v", got[0])
	}
	drops[0].Count = 7
	if got := book.Annotate(drops); got[0].Known || got[0].New != 3 {
		t.Errorf("grown = %+v", got[0])
	}
}

func TestQuarantineStaysKnownAcrossRestarts(t *testing.T) {
	db := openDB(t)
	book, err := health.Open(db, func() time.Time { return time.Unix(100, 0) })
	if err != nil {
		t.Fatal(err)
	}
	issues := []health.Issue{
		{Key: "unknown_signal/demo/x/metric", Source: health.SourceQuarantine, Count: 2},
		{Key: health.CounterPrefix + "intake.tooOld", Source: health.SourceCounter, Count: 1},
	}
	if err := book.Mark(issues, []string{issues[0].Key, issues[1].Key}, true, "anna"); err != nil {
		t.Fatal(err)
	}

	again, err := health.Open(db, nil)
	if err != nil {
		t.Fatal(err)
	}
	issues[0].Count = 50
	got := again.Annotate(issues)
	if !got[0].Known || got[0].KnownBy != "anna" || got[0].New != 0 {
		t.Errorf("quarantine after restart = %+v", got[0])
	}
	if got[1].Known {
		t.Errorf("a counter acknowledgement outlived the process: %+v", got[1])
	}
	if health.Unknown(got) != 1 {
		t.Errorf("unknown = %d", health.Unknown(got))
	}
}
