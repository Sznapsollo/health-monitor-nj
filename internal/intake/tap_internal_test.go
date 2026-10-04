package intake

import (
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestTheTapKeepsTheNewestAndCutsLongDatagrams(t *testing.T) {
	now := time.Now()
	tap := &Tap{}
	tap.Watch(now)
	from := netip.MustParseAddr("10.0.0.5")
	for i := range TapSize + 10 {
		tap.record(now, []byte(strconv.Itoa(i)), from, 8082, "example", "", "customWarning", "")
	}
	got, seq := tap.Since(0)
	if len(got) != TapSize || seq != TapSize+10 || got[0].Raw != "10" || got[len(got)-1].Raw != strconv.Itoa(TapSize+9) {
		t.Fatalf("kept %d up to %d, first %q", len(got), seq, got[0].Raw)
	}
	if got, _ := tap.Since(seq + 100); len(got) != TapSize {
		t.Fatalf("a seq from before a restart read %d, want all", len(got))
	}

	tap.record(now, []byte(strings.Repeat("x", TapRaw+1)), from, 8082, "example", "", "customWarning", "")
	last, _ := tap.Since(seq)
	if len(last) != 1 || !last[0].Cut || len(last[0].Raw) != TapRaw || last[0].Size != TapRaw+1 {
		t.Fatalf("long datagram = cut %v, %d of %d bytes", last[0].Cut, len(last[0].Raw), last[0].Size)
	}

	later := now.Add(TapWindow + time.Second)
	tap.record(later, []byte("late"), from, 8082, "example", "", "customWarning", "")
	tap.Watch(later)
	if got, _ := tap.Since(0); len(got) != 0 {
		t.Fatalf("after a pause = %d entries, want a fresh start", len(got))
	}
}
