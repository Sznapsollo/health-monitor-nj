package silence_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/silence"
	"github.com/Sznapsollo/health-monitor-nj/internal/store"
)

var base = time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)

type clock struct{ t time.Time }

func (c *clock) now() time.Time      { return c.t }
func (c *clock) add(d time.Duration) { c.t = c.t.Add(d) }

func open(t *testing.T) (*silence.Store, *clock, *store.Store) {
	t.Helper()
	db, err := store.Open(store.Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	c := &clock{t: base}
	s, err := silence.Open(db.DB(), c.now)
	if err != nil {
		t.Fatal(err)
	}
	return s, c, db
}

func TestSnoozeExpiresByItself(t *testing.T) {
	s, c, _ := open(t)
	ctx := context.Background()

	_, err := s.Create(ctx, silence.Silence{
		Platform: "test", Target: "status:servers/web-2", Kind: silence.KindSnooze,
		Reason: "maintenance", By: "anna", Until: base.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, ok := s.Match("test", "status:servers/web-2"); !ok {
		t.Fatal("the snooze is not in force")
	}
	c.add(2 * time.Hour)
	if _, ok := s.Match("test", "status:servers/web-2"); ok {
		t.Error("the snooze outlived its time")
	}
	if got := s.List(ctx, "test"); len(got) != 0 {
		t.Errorf("list = %+v, want the expired snooze gone", got)
	}
}

func TestMuteLastsAndIsReviewed(t *testing.T) {
	s, c, _ := open(t)
	ctx := context.Background()

	_, err := s.Create(ctx, silence.Silence{
		Platform: "test", Target: "signal:requests", Kind: silence.KindMute,
		Reason: "known issue", By: "bob",
	})
	if err != nil {
		t.Fatal(err)
	}

	c.add(10 * 24 * time.Hour)
	if _, ok := s.Match("test", "signal:requests"); !ok {
		t.Fatal("the mute stopped applying")
	}
	if got := s.NeedingReview(); len(got) != 0 {
		t.Errorf("review = %+v, want none after ten days", got)
	}

	c.add(25 * 24 * time.Hour)
	got := s.NeedingReview()
	if len(got) != 1 || got[0].Reason != "known issue" {
		t.Fatalf("review = %+v, want the forgotten mute after a month", got)
	}
}

func TestAReasonIsRequired(t *testing.T) {
	s, _, _ := open(t)
	_, err := s.Create(context.Background(), silence.Silence{
		Platform: "test", Target: "signal:requests", Kind: silence.KindMute,
	})
	if err == nil || !strings.Contains(err.Error(), "reason") {
		t.Fatalf("error = %v, want it to insist on a reason", err)
	}
}

func TestSnoozeNeedsAnExpiry(t *testing.T) {
	s, _, _ := open(t)
	_, err := s.Create(context.Background(), silence.Silence{
		Platform: "test", Target: "signal:requests", Kind: silence.KindSnooze, Reason: "x",
	})
	if err == nil || !strings.Contains(err.Error(), "expire") {
		t.Fatalf("error = %v", err)
	}
}

func TestTargetsAreTriedMostSpecificFirst(t *testing.T) {
	s, _, _ := open(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, silence.Silence{
		Platform: "test", Target: "platform:test", Kind: silence.KindMute, Reason: "whole env down",
	}); err != nil {
		t.Fatal(err)
	}

	targets := silence.Targets("test", "servers", "web-2", "latency/default", "")
	got, ok := s.Match("test", targets...)
	if !ok || got.Target != "platform:test" {
		t.Fatalf("match = %+v, ok = %v", got, ok)
	}
}

func TestMessagePatternSilence(t *testing.T) {
	s, _, _ := open(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, silence.Silence{
		Platform: "test", Target: silence.MatchTarget("securityService.isLoggedIn"),
		Kind: silence.KindMute, Reason: "known noise",
	}); err != nil {
		t.Fatal(err)
	}

	if _, ok := s.MatchesMessage("test", "securityService.isLoggedIn returned false"); !ok {
		t.Error("the pattern did not match")
	}
	if _, ok := s.MatchesMessage("test", "something else"); ok {
		t.Error("an unrelated message was silenced")
	}
}

func TestSuppressionIsCounted(t *testing.T) {
	s, _, db := open(t)
	ctx := context.Background()
	sil, err := s.Create(ctx, silence.Silence{
		Platform: "test", Target: "signal:requests", Kind: silence.KindMute, Reason: "noisy",
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		s.Suppressed(sil.ID)
	}
	list := s.List(ctx, "test")
	if len(list) != 1 || list[0].Suppressed != 3 {
		t.Fatalf("list = %+v, want the suppressed count", list)
	}

	if err := s.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	var stored int64
	if err := db.DB().QueryRow(`SELECT suppressed FROM silence WHERE id = ?`, sil.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != 3 {
		t.Fatalf("stored suppressed = %d, want 3", stored)
	}
}

func TestSilencesSurviveARestart(t *testing.T) {
	db, err := store.Open(store.Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	c := &clock{t: base}
	first, err := silence.Open(db.DB(), c.now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.Create(context.Background(), silence.Silence{
		Platform: "test", Target: "status:servers/web-2", Kind: silence.KindMute,
		Reason: "decommissioned", By: "anna",
	}); err != nil {
		t.Fatal(err)
	}

	// A new process over the same database sees it.
	second, err := silence.Open(db.DB(), c.now)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := second.Match("test", "status:servers/web-2")
	if !ok || got.Reason != "decommissioned" || got.By != "anna" {
		t.Fatalf("match after restart = %+v, ok = %v", got, ok)
	}
}

func TestDeleteUnsilences(t *testing.T) {
	s, _, _ := open(t)
	ctx := context.Background()
	sil, err := s.Create(ctx, silence.Silence{
		Platform: "test", Target: "signal:requests", Kind: silence.KindMute, Reason: "x",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, sil.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Match("test", "signal:requests"); ok {
		t.Error("the silence outlived its deletion")
	}
}

func TestASilenceCanBeEditedAndKeepsItsHistory(t *testing.T) {
	s, c, db := open(t)
	ctx := context.Background()
	made, err := s.Create(ctx, silence.Silence{
		Platform: "test", Target: `match:contains="timeout"`, Kind: silence.KindSnooze,
		Until: c.now().Add(time.Hour), Reason: "known issue", By: "anna",
	})
	if err != nil {
		t.Fatal(err)
	}
	s.Suppressed(made.ID)
	c.add(time.Minute)

	edited, err := s.Update(ctx, made.ID, silence.Silence{
		Target: "category:Warning JOB", Kind: silence.KindMute, Reason: "don't need to see it",
	})
	if err != nil {
		t.Fatal(err)
	}
	if edited.ID != made.ID || edited.By != "anna" || !edited.Created.Equal(made.Created) ||
		edited.Suppressed != 1 || !edited.Until.IsZero() || edited.Target != "category:Warning JOB" {
		t.Errorf("edited = %+v, want the same silence with the new settings", edited)
	}
	if _, ok := s.Match("test", "category:Warning JOB"); !ok {
		t.Error("the new target is not in force")
	}
	if _, ok := s.MatchesMessage("test", "a timeout"); ok {
		t.Error("the old target is still in force")
	}

	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := silence.Open(db.DB(), c.now)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = again.Close() }()
	if list := again.List(ctx, "test"); len(list) != 1 || list[0].Kind != silence.KindMute || list[0].Reason != "don't need to see it" {
		t.Errorf("after reopening = %+v, want the edit saved", list)
	}

	if _, err := s.Update(ctx, "sil-missing", silence.Silence{Target: "x", Kind: silence.KindMute, Reason: "x"}); !errors.Is(err, silence.ErrNotFound) {
		t.Errorf("editing a missing silence: %v", err)
	}
}
