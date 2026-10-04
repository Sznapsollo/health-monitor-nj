package core

import (
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/store"
)

type rolloverClock struct {
	start time.Time
	every time.Duration
}

// NextCheck is the first rollover at or after at that is still to come: when
// something that becomes due at at is actually archived, dropped or deleted.
func (c *Core) NextCheck(at time.Time) time.Time {
	c.mu.Lock()
	clock := c.checks
	c.mu.Unlock()
	if now := c.now(); at.Before(now) {
		at = now
	}
	if clock.start.IsZero() || clock.every <= 0 {
		return at
	}
	return nextTick(c.now(), at, c.now().Sub(clock.start), clock.every)
}

// nextTick is the first tick at or after at, for a ticker that has run for
// elapsed. The ticker counts time the machine was awake, which is what
// elapsed is when both ends carry Go's monotonic clock; after a suspend it
// is shorter than the wall time since the start, so the next tick is found
// from what the ticker has left, not from the start's wall time.
func nextTick(now, at time.Time, elapsed, every time.Duration) time.Time {
	next := now.Add(every - elapsed%every)
	if elapsed%every == 0 && elapsed > 0 {
		next = now
	}
	if !at.After(next) {
		return next
	}
	ticks := (at.Sub(next) + every - 1) / every
	return next.Add(ticks * every)
}

// Leaving is when one kind of a day's logs leaves the database, and whether
// it becomes an archive file or is dropped.
type Leaving struct {
	Kind    string    `json:"kind"`
	Name    string    `json:"name,omitempty"`
	On      time.Time `json:"on"`
	Archive bool      `json:"archive"`
}

// dueOn is the midnight (UTC) after which a day kept keep days is past it.
func dueOn(day string, keep int) time.Time {
	d, err := time.Parse("20060102", day)
	if err != nil {
		return time.Time{}
	}
	return d.AddDate(0, 0, keep+1)
}

// LogDayLeaves is when each of these kinds of a day's logs leaves the database.
func (c *Core) LogDayLeaves(day string, kinds []string) []Leaving {
	logging := c.logRules()
	names := c.LogNames()
	out := make([]Leaving, 0, len(kinds))
	for _, kind := range kinds {
		out = append(out, Leaving{
			Kind:    kind,
			Name:    names[kind],
			On:      c.NextCheck(dueOn(day, logging.keepDaysFor(kind))),
			Archive: logging.archives(kind),
		})
	}
	return out
}

// AlertDayLeaves is when a day of alerts leaves the database, and whether it
// becomes an archive file.
func (c *Core) AlertDayLeaves(day string) (time.Time, bool) {
	logging := c.logRules()
	return c.NextCheck(dueOn(day, logging.AlertDays)), logging.archivesAlerts()
}

// FileDeleteOn is when an archive file is deleted; false when it is kept.
func (c *Core) FileDeleteOn(f store.ArchiveFile) (time.Time, bool) {
	days := c.FileDays(f)
	if days <= 0 {
		return time.Time{}, false
	}
	return c.NextCheck(f.Written.AddDate(0, 0, days)), true
}
