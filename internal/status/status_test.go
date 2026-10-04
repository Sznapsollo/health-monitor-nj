package status_test

import (
	"context"
	"testing"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/status"
	"github.com/Sznapsollo/health-monitor-nj/internal/store"
)

var base = time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)

type clock struct{ t time.Time }

func (c *clock) now() time.Time      { return c.t }
func (c *clock) add(d time.Duration) { c.t = c.t.Add(d) }

func open(t *testing.T) (*status.Store, *clock, *store.Store) {
	t.Helper()
	db, err := store.Open(store.Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	c := &clock{t: base}
	s, err := status.Open(db.DB(), c.now)
	if err != nil {
		t.Fatal(err)
	}
	return s, c, db
}

func TestHeartbeatThenOffline(t *testing.T) {
	s, c, _ := open(t)
	s.Observe("test", "servers", "web-1", map[string]any{"build": "abc"}, c.now())

	if newly := s.CheckOffline(func(string) time.Duration { return 5 * time.Minute }); len(newly) != 0 {
		t.Fatalf("newly offline = %+v, want none while it is fresh", newly)
	}

	c.add(10 * time.Minute)
	newly := s.CheckOffline(func(string) time.Duration { return 5 * time.Minute })
	if len(newly) != 1 || newly[0].Key != "web-1" {
		t.Fatalf("newly offline = %+v", newly)
	}
	if !newly[0].Offline || newly[0].OfflineSince.IsZero() {
		t.Errorf("entity = %+v, want it marked offline with a time", newly[0])
	}

	// Only once: the second check has nothing new to say.
	if again := s.CheckOffline(func(string) time.Duration { return 5 * time.Minute }); len(again) != 0 {
		t.Errorf("newly offline = %+v, want each entity reported once", again)
	}
}

func TestRecoveryIsReported(t *testing.T) {
	s, c, _ := open(t)
	s.Observe("test", "servers", "web-1", nil, c.now())
	c.add(10 * time.Minute)
	s.CheckOffline(func(string) time.Duration { return 5 * time.Minute })

	_, recovered := s.Observe("test", "servers", "web-1", nil, c.now())
	if !recovered {
		t.Fatal("coming back was not reported as a recovery")
	}
	if list := s.List("test"); list[0].Offline {
		t.Errorf("entity = %+v, want it online again", list[0])
	}

	// A heartbeat from something that was never offline is not a recovery.
	if _, recovered := s.Observe("test", "servers", "web-1", nil, c.now()); recovered {
		t.Error("an ordinary heartbeat was reported as a recovery")
	}
}

func TestListPutsTroubleFirst(t *testing.T) {
	s, c, _ := open(t)
	s.Observe("test", "servers", "web-2", nil, c.now())
	s.Observe("test", "servers", "web-1", nil, c.now())
	c.add(10 * time.Minute)
	s.Observe("test", "servers", "web-1", nil, c.now())
	s.CheckOffline(func(string) time.Duration { return 5 * time.Minute })

	list := s.List("test")
	if len(list) != 2 || list[0].Key != "web-2" || !list[0].Offline {
		t.Fatalf("list = %+v, want the offline one first", list)
	}
	online, offline := s.Counts("test")
	if online != 1 || offline != 1 {
		t.Errorf("counts = %d online, %d offline", online, offline)
	}
}

func TestForgetRemovesForGood(t *testing.T) {
	s, c, _ := open(t)
	ctx := context.Background()
	s.Observe("test", "servers", "old-box", nil, c.now())

	if err := s.Forget(ctx, "test", "servers", "old-box"); err != nil {
		t.Fatalf("forget: %v", err)
	}
	if len(s.List("test")) != 0 {
		t.Fatal("the entity survived being forgotten")
	}
	c.add(time.Hour)
	if newly := s.CheckOffline(func(string) time.Duration { return time.Minute }); len(newly) != 0 {
		t.Errorf("a forgotten entity still alerted: %+v", newly)
	}

	// If it ever reports again it comes back as new.
	s.Observe("test", "servers", "old-box", nil, c.now())
	if len(s.List("test")) != 1 {
		t.Error("the entity did not reappear after reporting again")
	}
}

func TestEntitiesSurviveARestart(t *testing.T) {
	db, err := store.Open(store.Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	c := &clock{t: base}

	first, err := status.Open(db.DB(), c.now)
	if err != nil {
		t.Fatal(err)
	}
	first.Observe("test", "servers", "web-1", map[string]any{"build": "abc1234"}, c.now())
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := status.Open(db.DB(), c.now)
	if err != nil {
		t.Fatal(err)
	}
	list := second.List("test")
	if len(list) != 1 || list[0].Payload["build"] != "abc1234" {
		t.Fatalf("after restart = %+v, want the server remembered", list)
	}

	// And it can go offline based on when it was last seen before the restart.
	c.add(time.Hour)
	if newly := second.CheckOffline(func(string) time.Duration { return 5 * time.Minute }); len(newly) != 1 {
		t.Errorf("newly offline = %+v, want the remembered server", newly)
	}
}

func TestTargetIsTheSilenceId(t *testing.T) {
	e := status.Entity{Signal: "servers", Key: "web-2"}
	if e.Target() != "status:servers/web-2" {
		t.Errorf("target = %q", e.Target())
	}
}

func TestHeartbeatsAreWrittenLaterAndOnce(t *testing.T) {
	s, c, db := open(t)
	ctx := context.Background()
	for i := 0; i < 50; i++ {
		s.Observe("test", "servers", "web-1", map[string]any{"n": i}, c.now())
	}
	var rows int
	if err := db.DB().QueryRow(`SELECT COUNT(*) FROM status`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("rows = %d before the writer ran, want 0", rows)
	}
	if err := s.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if got := s.Stats(); got.Written != 1 || got.Pending != 0 {
		t.Fatalf("stats = %+v, want fifty heartbeats written as one row", got)
	}
	var payload string
	if err := db.DB().QueryRow(`SELECT payload FROM status WHERE key = 'web-1'`).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if payload != `{"n":49}` {
		t.Fatalf("payload = %s, want the latest heartbeat", payload)
	}
}

func TestForgetBeatsAPendingHeartbeat(t *testing.T) {
	s, c, db := open(t)
	ctx := context.Background()
	s.Observe("test", "servers", "old-box", nil, c.now())
	if err := s.Forget(ctx, "test", "servers", "old-box"); err != nil {
		t.Fatal(err)
	}
	if err := s.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := db.DB().QueryRow(`SELECT COUNT(*) FROM status`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("rows = %d, want the forgotten server to stay forgotten", rows)
	}
}

func TestAServerStillOfflineIsNotNewlyOfflineAfterARestart(t *testing.T) {
	db, err := store.Open(store.Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	c := &clock{t: base}

	first, err := status.Open(db.DB(), c.now)
	if err != nil {
		t.Fatal(err)
	}
	first.Observe("test", "servers", "web-1", nil, c.now())
	c.add(time.Hour)
	if newly := first.CheckOffline(func(string) time.Duration { return 5 * time.Minute }); len(newly) != 1 {
		t.Fatalf("newly offline = %+v", newly)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := status.Open(db.DB(), c.now)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()
	if list := second.List("test"); len(list) != 1 || !list[0].Offline || !list[0].OfflineSince.Equal(c.now()) {
		t.Errorf("after restart = %+v, want it remembered as offline since the check", list)
	}
	if newly := second.CheckOffline(func(string) time.Duration { return 5 * time.Minute }); len(newly) != 0 {
		t.Errorf("newly offline after restart = %+v, want none", newly)
	}

	second.Observe("test", "servers", "web-1", nil, c.now())
	if err := second.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	third, err := status.Open(db.DB(), c.now)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = third.Close() }()
	if list := third.List("test"); list[0].Offline {
		t.Errorf("after recovery and restart = %+v, want it online", list)
	}
}

func TestAStatusTableFromBeforeOfflineWasKeptStillOpens(t *testing.T) {
	db, err := store.Open(store.Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.DB().Exec(`CREATE TABLE status (platform TEXT NOT NULL, signal TEXT NOT NULL, key TEXT NOT NULL,
		payload TEXT NOT NULL, last_seen INTEGER NOT NULL, PRIMARY KEY (platform, signal, key)) WITHOUT ROWID;
		INSERT INTO status VALUES ('test', 'servers', 'web-1', '{}', 1)`); err != nil {
		t.Fatal(err)
	}
	s, err := status.Open(db.DB(), (&clock{t: base}).now)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if list := s.List("test"); len(list) != 1 || list[0].Offline {
		t.Errorf("list = %+v, want the old row loaded as online", list)
	}
}
