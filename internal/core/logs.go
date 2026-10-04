package core

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/protocol"
	"github.com/Sznapsollo/health-monitor-nj/internal/signal"
	"github.com/Sznapsollo/health-monitor-nj/internal/store"
)

// Logging is where searchable rows go. Without it, log envelopes are counted
// and dropped, which is what Phases 1 and 2 did.
type Logging struct {
	Writer *store.LogWriter
	// KeepDays is how long a day of logs is kept before its table is dropped,
	// for any kind KeepDaysByKind does not name.
	KeepDays int
	// KeepDaysByKind is the per-kind override. The bulky kinds are worth
	// keeping for less time, which the split into one table per kind makes
	// possible.
	KeepDaysByKind map[string]int
	// ArchiveDir is where days past KeepDays are moved as gzip files; empty
	// drops them instead.
	ArchiveDir string
	// ArchiveKeepDays is how long a day's file is kept once the day has left
	// the database; 0 drops the day instead of archiving it.
	ArchiveKeepDays       int
	ArchiveKeepDaysByKind map[string]int
	// ActivityDays is how long the per-user activity summary is kept.
	ActivityDays int
	// AlertDays is how long the alert history is kept in the database.
	AlertDays int
	// AlertArchiveDays is how long a day of alerts is kept as a file after
	// that; 0 deletes the alerts instead.
	AlertArchiveDays int
}

// keepDaysFor is how long one kind of log is kept.
func (l Logging) keepDaysFor(signal string) int {
	if n, ok := l.KeepDaysByKind[signal]; ok && n > 0 {
		return n
	}
	return l.KeepDays
}

func (l Logging) archiveDaysFor(signal string) int {
	if n, ok := l.ArchiveKeepDaysByKind[signal]; ok {
		return n
	}
	return l.ArchiveKeepDays
}

func (l Logging) archives(signal string) bool {
	return l.ArchiveDir != "" && l.archiveDaysFor(signal) > 0
}

func (l Logging) archivesAlerts() bool {
	return l.ArchiveDir != "" && l.AlertArchiveDays > 0
}

// FileDays is how long an archive file is kept after it was written; 0 keeps it.
func (l Logging) FileDays(f store.ArchiveFile) int {
	if f.Alerts {
		return l.AlertArchiveDays
	}
	return l.archiveDaysFor(f.Kind)
}

// SetLogging attaches the log writer.
func (c *Core) SetLogging(l Logging) {
	if l.KeepDays <= 0 {
		l.KeepDays = 14
	}
	if l.ActivityDays <= 0 {
		l.ActivityDays = 31
	}
	if l.AlertDays <= 0 {
		l.AlertDays = 14
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.logging = l
}

// writeLogs turns log envelopes into stored rows, one write for the batch. The fields worth searching
// by name are lifted out of the payload so a query can use an index rather
// than the full-text scan.
func (c *Core) writeLogs(packets []protocol.Packet) {
	if len(packets) == 0 {
		return
	}
	c.mu.Lock()
	writer := c.logging.Writer
	c.mu.Unlock()
	if writer == nil {
		return
	}

	now := c.now()
	rows := make([]store.LogRow, 0, len(packets))
	for _, p := range packets {
		at := p.TS.Time
		if at.IsZero() {
			at = now
		}
		data := p.Log.Data
		rows = append(rows, store.LogRow{
			Platform: p.Platform,
			Signal:   p.Log.Signal,
			TS:       at,
			Key:      p.Log.Key,
			Account:  text(data, "account"),
			User:     text(data, "user"),
			URL:      text(data, "url"),
			Level:    string(p.Log.Level),
			Payload:  data,
		})
	}
	writer.Write(rows)
}

func text(data map[string]any, key string) string {
	if v, ok := data[key].(string); ok {
		return v
	}
	return ""
}

// Rollover computes yesterday's and today's activity summaries and drops logs
// past their retention. It is the nightly job, though running it more often
// costs little and keeps the numbers fresh.
func (c *Core) Rollover(ctx context.Context, db *store.Store) {
	logging := c.logRules()
	if db == nil {
		return
	}

	now := c.now().UTC()
	for _, day := range []string{now.Format("20060102"), now.AddDate(0, 0, -1).Format("20060102")} {
		if _, err := db.RefreshActivity(ctx, day, now); err != nil {
			c.log.Error("could not compute user activity", "day", day, "error", err)
		}
	}

	c.expireLogs(ctx, db, logging, now)

	activityCutoff := now.AddDate(0, 0, -logging.ActivityDays).Format("20060102")
	if err := db.PurgeActivity(ctx, activityCutoff); err != nil {
		c.log.Error("could not purge user activity", "error", err)
	}

	c.expireAlerts(ctx, db, logging, now)
	c.purgeArchives(logging, now)
	c.reclaim(ctx, db)
	db.MeasureLogTables(ctx, now.Format("20060102"))
}

// expireAlerts moves days of alert history past AlertDays to archive files,
// or deletes them when alerts are not archived.
func (c *Core) expireAlerts(ctx context.Context, db *store.Store, logging Logging, now time.Time) {
	cutoff := dayBefore(now, logging.AlertDays)
	if !logging.archivesAlerts() {
		if n, err := db.PurgeAlerts(ctx, cutoff); err != nil {
			c.log.Error("could not purge alert history", "error", err)
		} else if n > 0 {
			c.log.Info("purged alert history", "rows", n, "keep_days", logging.AlertDays)
		}
		return
	}
	days, err := db.AlertHistoryDays(ctx)
	if err != nil {
		c.log.Error("could not list alert days", "error", err)
		return
	}
	for _, day := range days {
		if day >= cutoff {
			break
		}
		f, err := db.ArchiveAlertDay(ctx, day, logging.ArchiveDir)
		if err != nil {
			c.log.Error("could not archive a day of alerts; it is kept", "day", day, "error", err)
			continue
		}
		c.log.Info("archived a day of alerts", "day", day, "file", f.File, "KB", f.Bytes>>10)
	}
}

func (c *Core) purgeArchives(logging Logging, now time.Time) {
	if logging.ArchiveDir == "" {
		return
	}
	removed, err := store.PurgeArchives(logging.ArchiveDir, now, logging.FileDays)
	if err != nil {
		c.log.Error("could not delete old archive files", "error", err)
	}
	for _, f := range removed {
		c.log.Info("deleted an archive file", "file", f.File)
	}
}

// Each kind of log expires on its own clock, which is what one table per day
// per kind buys: a drop, never a delete scan.
func (c *Core) expireLogs(ctx context.Context, db *store.Store, logging Logging, now time.Time) {
	c.logDaysMu.Lock()
	defer c.logDaysMu.Unlock()
	tables, err := db.LogTables(ctx)
	if err != nil {
		c.log.Error("could not list log tables", "error", err)
		return
	}
	summed := map[string]bool{}
	for _, t := range tables {
		keep := logging.keepDaysFor(t.Signal)
		if t.Day >= dayBefore(now, keep) {
			continue
		}
		if !summed[t.Day] {
			summed[t.Day] = true
			c.sumActivity(ctx, db, t.Day)
		}
		if logging.archives(t.Signal) {
			_ = c.archiveTable(ctx, db, logging, t)
			continue
		}
		if err := db.DropLogTable(ctx, t); err != nil {
			c.log.Error("could not drop a day of logs",
				"day", t.Day, "kind", t.Signal, "error", err)
			continue
		}
		c.log.Info("dropped a day of logs", "day", t.Day, "kind", t.Signal, "keep_days", keep)
	}

}

// sumActivity works out a day's activity from its logs while they are still
// all in the database.
func (c *Core) sumActivity(ctx context.Context, db *store.Store, day string) {
	if _, err := db.ComputeActivity(ctx, day); err != nil {
		c.log.Error("could not compute user activity", "day", day, "error", err)
	}
}

func (c *Core) archiveTable(ctx context.Context, db *store.Store, logging Logging, t store.LogTable) error {
	started := time.Now()
	f, err := db.ArchiveLogTable(ctx, t, logging.ArchiveDir)
	if err != nil {
		c.log.Error("could not archive a day of logs; the table is kept",
			"day", t.Day, "kind", t.Signal, "error", err)
		return err
	}
	c.log.Info("archived a day of logs", "day", t.Day, "kind", t.Signal, "file", f.File,
		"MB", float64(f.Bytes*10>>20)/10, "took", time.Since(started).Round(time.Millisecond).String())
	return nil
}

func dayBefore(now time.Time, days int) string {
	return now.UTC().AddDate(0, 0, -days).Format("20060102")
}

// Retention is how long one kind of log stays in the database and then as an
// archive file; ArchiveDays is 0 when that kind is dropped, not archived.
type Retention struct {
	Kind string `json:"kind,omitempty"`
	// Name is the display name of the log signal defining the kind, if any.
	Name        string `json:"name,omitempty"`
	DBDays      int    `json:"dbDays"`
	ArchiveDays int    `json:"archiveDays"`
}

func (l Logging) retentionOf(kind string) Retention {
	r := Retention{Kind: kind, DBDays: l.keepDaysFor(kind)}
	if l.archives(kind) {
		r.ArchiveDays = l.archiveDaysFor(kind)
	}
	return r
}

// LogRetention is the rule for kinds not named anywhere, and the rule for each
// kind listed in kinds or named in the configuration.
func (c *Core) LogRetention(kinds []string) (Retention, []Retention) {
	logging := c.logRules()
	named := map[string]bool{}
	for _, k := range kinds {
		named[k] = true
	}
	c.mu.Lock()
	for k := range c.logging.KeepDaysByKind {
		named[k] = true
	}
	for k := range c.logging.ArchiveKeepDaysByKind {
		named[k] = true
	}
	c.mu.Unlock()
	fallback := logging.retentionOf("")
	names := c.LogNames()
	var out []Retention
	for k := range named {
		r := logging.retentionOf(k)
		r.Name = names[k]
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Kind < out[j].Kind })
	return fallback, out
}

// ConfiguredRetention is what config.yaml alone gives a kind of log: what a
// log signal falls back to where it sets no days of its own.
func (c *Core) ConfiguredRetention(kind string) Retention {
	c.mu.Lock()
	l := c.logging
	c.mu.Unlock()
	return l.retentionOf(kind)
}

// FileDays is how long an archive file is kept after it was written.
func (c *Core) FileDays(f store.ArchiveFile) int {
	return c.logRules().FileDays(f)
}

// LogNames maps each kind of log defined as a signal to its display name.
func (c *Core) LogNames() map[string]string {
	out := map[string]string{}
	for _, def := range c.reg.All() {
		if def.Kind == signal.KindLog && def.Display.Name != "" && def.Display.Name != def.Name {
			out[def.Name] = def.Display.Name
		}
	}
	return out
}

// logRules is the configured retention with each log signal's own days laid
// over it: a kind defined as a signal is kept as that signal says.
func (c *Core) logRules() Logging {
	c.mu.Lock()
	l := c.logging
	c.mu.Unlock()
	keep, archive := map[string]int{}, map[string]int{}
	for k, v := range l.KeepDaysByKind {
		keep[k] = v
	}
	for k, v := range l.ArchiveKeepDaysByKind {
		archive[k] = v
	}
	for _, def := range c.reg.All() {
		if def.Kind != signal.KindLog {
			continue
		}
		if def.Retention.LogDays > 0 {
			keep[def.Name] = def.Retention.LogDays
		}
		if def.Retention.ArchiveDays != nil {
			archive[def.Name] = *def.Retention.ArchiveDays
		}
	}
	l.KeepDaysByKind, l.ArchiveKeepDaysByKind = keep, archive
	return l
}

// AlertRetention is how long alerts stay in the database, then as files.
func (c *Core) AlertRetention() Retention {
	c.mu.Lock()
	defer c.mu.Unlock()
	r := Retention{Kind: store.AlertsKind, DBDays: c.logging.AlertDays}
	if c.logging.archivesAlerts() {
		r.ArchiveDays = c.logging.AlertArchiveDays
	}
	return r
}

// ErrToday is refused by the hand-made actions: today's tables are still
// being written.
var ErrToday = errors.New("today's logs are still being written")

// ErrNoArchive means archiving is not configured.
var ErrNoArchive = errors.New("no archive directory is configured")

// ArchiveDir is where archive files are kept, empty when there are none.
func (c *Core) ArchiveDir() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.logging.ArchiveDir
}

// ArchiveLogDay moves every kind of one past day to archive files now,
// instead of when it ages out.
func (c *Core) ArchiveLogDay(ctx context.Context, db *store.Store, day string) ([]store.LogTable, error) {
	logging := c.logRules()
	if logging.ArchiveDir == "" {
		return nil, ErrNoArchive
	}
	tables, err := c.pastDay(ctx, db, day)
	if err != nil {
		return nil, err
	}
	c.logDaysMu.Lock()
	defer c.logDaysMu.Unlock()
	c.sumActivity(ctx, db, day)
	for _, t := range tables {
		if err := c.archiveTable(ctx, db, logging, t); err != nil {
			return nil, err
		}
	}
	return tables, nil
}

// ArchiveAlertDay moves one past day of alerts to its archive file now,
// instead of when it ages out.
func (c *Core) ArchiveAlertDay(ctx context.Context, db *store.Store, day string) (store.ArchiveFile, error) {
	c.mu.Lock()
	dir := c.logging.ArchiveDir
	c.mu.Unlock()
	if dir == "" {
		return store.ArchiveFile{}, ErrNoArchive
	}
	if day >= c.now().UTC().Format("20060102") {
		return store.ArchiveFile{}, ErrToday
	}
	f, err := db.ArchiveAlertDay(ctx, day, dir)
	if err != nil {
		return store.ArchiveFile{}, err
	}
	c.log.Info("archived a day of alerts by hand", "day", day, "file", f.File)
	return f, nil
}

// DropAlertDay deletes one past day of alerts without archiving it.
func (c *Core) DropAlertDay(ctx context.Context, db *store.Store, day string) (int64, error) {
	if day >= c.now().UTC().Format("20060102") {
		return 0, ErrToday
	}
	n, err := db.DeleteAlertDay(ctx, day)
	if err == nil {
		c.log.Info("deleted a day of alerts by hand", "day", day, "alerts", n)
	}
	return n, err
}

// DropLogDay deletes every kind of one past day without archiving it.
func (c *Core) DropLogDay(ctx context.Context, db *store.Store, day string) ([]store.LogTable, error) {
	tables, err := c.pastDay(ctx, db, day)
	if err != nil {
		return nil, err
	}
	c.logDaysMu.Lock()
	defer c.logDaysMu.Unlock()
	c.sumActivity(ctx, db, day)
	for _, t := range tables {
		if err := db.DropLogTable(ctx, t); err != nil {
			return nil, err
		}
		c.log.Info("deleted a day of logs by hand", "day", t.Day, "kind", t.Signal)
	}
	return tables, nil
}

// DropLogKind deletes every stored day of one kind of log, today's included:
// what a sender logged by mistake.
func (c *Core) DropLogKind(ctx context.Context, db *store.Store, kind string) ([]store.LogTable, error) {
	all, err := db.LogTables(ctx)
	if err != nil {
		return nil, err
	}
	c.logDaysMu.Lock()
	defer c.logDaysMu.Unlock()
	var dropped []store.LogTable
	for _, t := range all {
		if t.Signal != kind {
			continue
		}
		if err := db.DropLogTable(ctx, t); err != nil {
			return dropped, err
		}
		dropped = append(dropped, t)
		c.log.Info("deleted a day of logs by hand", "day", t.Day, "kind", t.Signal)
	}
	return dropped, nil
}

func (c *Core) pastDay(ctx context.Context, db *store.Store, day string) ([]store.LogTable, error) {
	if day >= c.now().UTC().Format("20060102") {
		return nil, ErrToday
	}
	all, err := db.LogTables(ctx)
	if err != nil {
		return nil, err
	}
	var out []store.LogTable
	for _, t := range all {
		if t.Day == day {
			out = append(out, t)
		}
	}
	return out, nil
}

// ReclaimNow gives freed space back to the disk in the background.
func (c *Core) ReclaimNow(db *store.Store) {
	go c.reclaim(context.Background(), db)
}

// RolloverEvery runs the rollover until ctx is cancelled.
func (c *Core) RolloverEvery(ctx context.Context, db *store.Store, every time.Duration) {
	if every <= 0 {
		every = time.Hour
	}
	c.mu.Lock()
	c.checks = rolloverClock{start: c.now(), every: every}
	c.mu.Unlock()
	t := time.NewTicker(every)
	defer t.Stop()
	// Once at start, so a restart does not leave yesterday uncounted.
	c.Rollover(ctx, db)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.Rollover(ctx, db)
		}
	}
}
