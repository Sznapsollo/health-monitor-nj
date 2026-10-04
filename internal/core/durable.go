package core

import (
	"context"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/signal"
	"github.com/Sznapsollo/health-monitor-nj/internal/state"
	"github.com/Sznapsollo/health-monitor-nj/internal/store"
)

// Durable is the half of the writer the core needs. Keeping it an interface
// means the core runs without a database in tests.
type Durable interface {
	Samples(samples []state.Sample)
}

// Restore fills the hot state from disk, so a restart does not leave the
// charts blank while the first minute accumulates.
func (c *Core) Restore(ctx context.Context, db *store.Store) error {
	now := c.now()
	nowMin := state.MinuteOf(now)

	var restored, signals, names int
	// The built-in packets signal joins on the first packet, after this runs.
	for _, platform := range c.reg.Platforms() {
		c.reg.Packets(platform)
	}
	for _, def := range c.reg.All() {
		if def.Kind != signal.KindTimeseries {
			continue
		}
		minutes, err := db.Load(ctx, store.Restore{
			Platform:   def.Platform,
			Signal:     def.Name,
			DetailFrom: nowMin - int64(def.Retention.HotDetailMinutes) + 1,
			TotalsFrom: nowMin - int64(def.Retention.HotTotalsMinutes) + 1,
			To:         nowMin,
			Pairs:      c.store.PairsOf(def.Platform, def.Name),
		})
		if err != nil {
			return err
		}
		n, err := c.restoreLabels(ctx, db, def)
		if err != nil {
			return err
		}
		names += n
		if len(minutes) == 0 {
			continue
		}
		c.store.Restore(def.Platform, def.Name, minutes)
		restored += len(minutes)
		signals++
	}
	if restored > 0 || names > 0 {
		c.log.Info("hot state restored", "signals", signals, "minutes", restored, "names", names,
			"took_ms", time.Since(now).Milliseconds())
	}
	return nil
}

func (c *Core) restoreLabels(ctx context.Context, db *store.Store, def *signal.Definition) (int, error) {
	if len(def.Display.Labels) == 0 {
		return 0, nil
	}
	labels, err := db.Labels(ctx, def.Platform, def.Name)
	if err != nil {
		return 0, err
	}
	kept := labels[:0]
	for _, l := range labels {
		if _, ok := def.Display.Labels[l.Dim]; ok {
			kept = append(kept, l)
		}
	}
	c.store.RestoreLabels(def.Platform, def.Name, kept)
	return len(kept), nil
}

// Purge applies each signal's durable retention. It is cheap enough to run on
// a timer rather than at a particular hour.
func (c *Core) Purge(ctx context.Context, db *store.Store) {
	nowMin := state.MinuteOf(c.now())
	for _, def := range c.reg.All() {
		if def.Retention.DurableDays <= 0 {
			continue
		}
		if def.Retention.DetailDays > 0 {
			n, err := db.Rollup(ctx, def.Platform, def.Name, nowMin-int64(def.Retention.DetailDays)*24*60)
			if err != nil {
				c.log.Error("could not roll up old aggregates",
					"platform", def.Platform, "signal", def.Name, "error", err)
			} else if n > 0 {
				c.log.Info("rolled up old aggregates to hours",
					"platform", def.Platform, "signal", def.Name, "rows", n, "days", def.Retention.DetailDays)
			}
		}
		before := nowMin - int64(def.Retention.DurableDays)*24*60
		n, err := db.Purge(ctx, def.Platform, def.Name, before)
		if err != nil {
			c.log.Error("could not purge old aggregates",
				"platform", def.Platform, "signal", def.Name, "error", err)
			continue
		}
		if n > 0 {
			c.log.Info("purged old aggregates",
				"platform", def.Platform, "signal", def.Name, "rows", n, "days", def.Retention.DurableDays)
		}
	}
	c.purgeUndeclared(ctx, db, nowMin)
	c.reclaim(ctx, db)
}

// purgeUndeclared applies the default retention to signals no catalogue
// declares any more. Recent rows stay, so a signal that is only briefly
// missing from a catalogue loses nothing.
func (c *Core) purgeUndeclared(ctx context.Context, db *store.Store, nowMin int64) {
	stored, err := db.StoredSignals(ctx)
	if err != nil {
		c.log.Error("could not list stored signals", "error", err)
		return
	}
	before := nowMin - int64(signal.DefaultDurableDays)*24*60
	for _, st := range stored {
		if _, ok := c.reg.Lookup(st.Platform, st.Signal); ok {
			continue
		}
		n, err := db.Purge(ctx, st.Platform, st.Signal, before)
		if err != nil {
			c.log.Error("could not purge an undeclared signal", "platform", st.Platform, "signal", st.Signal, "error", err)
			continue
		}
		if n > 0 {
			c.log.Info("purged old aggregates of an undeclared signal",
				"platform", st.Platform, "signal", st.Signal, "rows", n, "days", signal.DefaultDurableDays)
		}
	}
}

// reclaim gives the space freed by purges and dropped tables back to the disk.
func (c *Core) reclaim(ctx context.Context, db *store.Store) {
	freed, err := db.ReclaimSpace(ctx)
	if err != nil {
		c.log.Error("could not return freed space to the disk", "error", err)
		return
	}
	if freed > 0 {
		c.log.Info("returned freed space to the disk", "MB", float64(freed*10>>20)/10)
	}
}

// PurgeEvery runs Purge until ctx is cancelled.
func (c *Core) PurgeEvery(ctx context.Context, db *store.Store, every time.Duration) {
	if every <= 0 {
		every = time.Hour
	}
	t := time.NewTicker(every)
	defer t.Stop()
	// Once at start, so deploys more often than every hour still purge.
	c.Purge(ctx, db)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.Purge(ctx, db)
		}
	}
}
