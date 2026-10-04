package core

import (
	"testing"
	"time"
)

func TestTheNextCheckAfterASuspendCountsTheTimeAwake(t *testing.T) {
	now := time.Date(2026, 9, 28, 5, 4, 0, 0, time.UTC)
	elapsed := 25 * time.Minute
	wantFirst := time.Date(2026, 9, 28, 5, 39, 0, 0, time.UTC)

	overdue := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	if got := nextTick(now, overdue, elapsed, time.Hour); !got.Equal(wantFirst) {
		t.Errorf("an overdue day goes %v, want the first check after waking, %v", got, wantFirst)
	}
	tomorrow := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	if got := nextTick(now, tomorrow, elapsed, time.Hour); !got.Equal(time.Date(2026, 9, 29, 0, 39, 0, 0, time.UTC)) {
		t.Errorf("tomorrow's day goes %v, want the :39 check after midnight", got)
	}
}
