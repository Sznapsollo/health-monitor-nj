package protocol

import (
	"errors"
	"strings"
	"time"
)

// layouts accepted for the ts field. The first is what Java's
// yyyy-MM-dd'T'HH:mm:ss.SSSZ produces (offset without a colon), the rest are
// the ISO-8601 forms every other language emits.
var layouts = []string{
	"2006-01-02T15:04:05.000-0700",
	"2006-01-02T15:04:05-0700",
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02T15:04:05.000",
	"2006-01-02T15:04:05",
}

// Timestamp is an event time that accepts every layout senders use in the
// wild. The zero value means "not supplied"; the intake substitutes server
// time.
type Timestamp struct {
	time.Time
}

// NewTimestamp wraps t.
func NewTimestamp(t time.Time) Timestamp { return Timestamp{Time: t} }

// ParseTime parses an event time in any accepted layout; one without an offset
// is the sender's wall clock, read in the server's zone. The layout is picked
// from how the string ends, so the usual case costs one parse; a failed parse
// allocates its error, and trying the list in order paid for that up to twice.
func ParseTime(s string) (time.Time, error) {
	if l := layoutFor(s); l != "" {
		if t, err := time.ParseInLocation(l, s, time.Local); err == nil {
			return t, nil
		}
	}
	for _, l := range layouts {
		if t, err := time.ParseInLocation(l, s, time.Local); err == nil {
			return t, nil
		}
	}
	return time.Time{}, errors.New("protocol: unrecognised timestamp " + s)
}

// layoutFor chooses by the offset at the end. Parsing accepts a fractional
// second after the seconds even when the layout has none, so each covers the
// forms with and without milliseconds.
func layoutFor(s string) string {
	n := len(s)
	switch {
	case n < 19:
		return ""
	case s[n-1] == 'Z', n >= 25 && s[n-3] == ':' && (s[n-6] == '+' || s[n-6] == '-'):
		return time.RFC3339
	case n >= 24 && (s[n-5] == '+' || s[n-5] == '-') && digits(s[n-4:]):
		return "2006-01-02T15:04:05-0700"
	default:
		return "2006-01-02T15:04:05"
	}
}

func digits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// UnmarshalJSON accepts a quoted timestamp in any accepted layout, an epoch
// value in milliseconds, or null.
func (ts *Timestamp) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "null" || s == `""` {
		ts.Time = time.Time{}
		return nil
	}
	if s[0] != '"' {
		ms, err := parseInt(s)
		if err != nil {
			return err
		}
		ts.Time = time.UnixMilli(ms)
		return nil
	}
	t, err := ParseTime(strings.Trim(s, `"`))
	if err != nil {
		return err
	}
	ts.Time = t
	return nil
}

// MarshalJSON writes RFC 3339 with milliseconds, or null for the zero value.
func (ts Timestamp) MarshalJSON() ([]byte, error) {
	if ts.Time.IsZero() {
		return []byte("null"), nil
	}
	return []byte(`"` + ts.Time.Format("2006-01-02T15:04:05.000Z07:00") + `"`), nil
}

func parseInt(s string) (int64, error) {
	var n int64
	neg := false
	for i, c := range s {
		if i == 0 && c == '-' {
			neg = true
			continue
		}
		if c < '0' || c > '9' {
			return 0, errors.New("protocol: not an epoch timestamp " + s)
		}
		n = n*10 + int64(c-'0')
	}
	if neg {
		n = -n
	}
	return n, nil
}
