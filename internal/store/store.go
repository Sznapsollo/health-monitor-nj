// Package store is the durable half of the server: per-minute aggregates that
// survive a restart and answer for windows older or deeper than the hot tiers.
// It is SQLite through the pure-Go driver, so the binary stays static and
// cross-compiles without cgo.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"
)

// schema is applied on open. Every statement is safe to run again.
const schema = `
-- Chart data, stored per dimension rather than per full combination of
-- dimensions: one row per minute for the total, one per value of each
-- dimension, one per value pair of the pairs kept. After a signal's
-- detail_days the value and pair rows are summed per hour (the *_hour tables,
-- keyed by the hour's first minute); totals stay per minute.
DROP TABLE IF EXISTS minute_agg;

CREATE TABLE IF NOT EXISTS agg_total (
    platform TEXT    NOT NULL,
    signal   TEXT    NOT NULL,
    minute   INTEGER NOT NULL,
    count    INTEGER NOT NULL,
    sum_ms   REAL    NOT NULL,
    min_ms   REAL    NOT NULL,
    max_ms   REAL    NOT NULL,
    samples  INTEGER NOT NULL,
    PRIMARY KEY (platform, signal, minute)
) WITHOUT ROWID;

CREATE TABLE IF NOT EXISTS agg_dim (
    platform TEXT    NOT NULL,
    signal   TEXT    NOT NULL,
    minute   INTEGER NOT NULL,
    dim      TEXT    NOT NULL,
    value    TEXT    NOT NULL,
    count    INTEGER NOT NULL,
    sum_ms   REAL    NOT NULL,
    min_ms   REAL    NOT NULL,
    max_ms   REAL    NOT NULL,
    samples  INTEGER NOT NULL,
    PRIMARY KEY (platform, signal, minute, dim, value)
) WITHOUT ROWID;

CREATE TABLE IF NOT EXISTS agg_pair (
    platform  TEXT    NOT NULL,
    signal    TEXT    NOT NULL,
    minute    INTEGER NOT NULL,
    dim       TEXT    NOT NULL,
    value     TEXT    NOT NULL,
    sub       TEXT    NOT NULL,
    sub_value TEXT    NOT NULL,
    count     INTEGER NOT NULL,
    sum_ms    REAL    NOT NULL,
    min_ms    REAL    NOT NULL,
    max_ms    REAL    NOT NULL,
    samples   INTEGER NOT NULL,
    PRIMARY KEY (platform, signal, minute, dim, sub, value, sub_value)
) WITHOUT ROWID;

CREATE TABLE IF NOT EXISTS agg_dim_hour (
    platform TEXT    NOT NULL,
    signal   TEXT    NOT NULL,
    minute   INTEGER NOT NULL,
    dim      TEXT    NOT NULL,
    value    TEXT    NOT NULL,
    count    INTEGER NOT NULL,
    sum_ms   REAL    NOT NULL,
    min_ms   REAL    NOT NULL,
    max_ms   REAL    NOT NULL,
    samples  INTEGER NOT NULL,
    PRIMARY KEY (platform, signal, minute, dim, value)
) WITHOUT ROWID;

CREATE TABLE IF NOT EXISTS agg_pair_hour (
    platform  TEXT    NOT NULL,
    signal    TEXT    NOT NULL,
    minute    INTEGER NOT NULL,
    dim       TEXT    NOT NULL,
    value     TEXT    NOT NULL,
    sub       TEXT    NOT NULL,
    sub_value TEXT    NOT NULL,
    count     INTEGER NOT NULL,
    sum_ms    REAL    NOT NULL,
    min_ms    REAL    NOT NULL,
    max_ms    REAL    NOT NULL,
    samples   INTEGER NOT NULL,
    PRIMARY KEY (platform, signal, minute, dim, sub, value, sub_value)
) WITHOUT ROWID;

-- The name each value of a labelled dimension last went by (account ->
-- accountName), so charts show names after a restart. seen is the minute the
-- name was last confirmed; rows go with the signal's durable retention.
CREATE TABLE IF NOT EXISTS dim_label (
    platform TEXT    NOT NULL,
    signal   TEXT    NOT NULL,
    dim      TEXT    NOT NULL,
    value    TEXT    NOT NULL,
    name     TEXT    NOT NULL,
    seen     INTEGER NOT NULL,
    PRIMARY KEY (platform, signal, dim, value)
) WITHOUT ROWID;

-- The rows and bytes of each finished day's log table, measured once so the
-- Storage tab never scans a table to show them; a row goes with its table.
CREATE TABLE IF NOT EXISTS log_table_stats (
    name     TEXT    PRIMARY KEY,
    rows     INTEGER NOT NULL,
    bytes    INTEGER NOT NULL,
    measured INTEGER NOT NULL
) WITHOUT ROWID;

-- One row per burst of an alert: the same row the Alerts tab shows, kept so it
-- can still be looked at tomorrow. Repeats inside a burst update the row's
-- count rather than adding rows, which is what keeps a flapping job from
-- filling the table.
CREATE TABLE IF NOT EXISTS alert_history (
    id             TEXT    PRIMARY KEY,
    platform       TEXT    NOT NULL,
    day            TEXT    NOT NULL,
    group_key      TEXT,
    signal         TEXT,
    level          TEXT    NOT NULL,
    category       TEXT,
    message        TEXT    NOT NULL,
    source         TEXT,
    target         TEXT,
    count          INTEGER NOT NULL,
    first_ts       INTEGER NOT NULL,
    last_ts        INTEGER NOT NULL,
    silenced       INTEGER NOT NULL DEFAULT 0,
    silence_reason TEXT,
    payload        TEXT
);

CREATE INDEX IF NOT EXISTS alert_history_by_time ON alert_history (platform, last_ts);
CREATE INDEX IF NOT EXISTS alert_history_by_day ON alert_history (day);
CREATE INDEX IF NOT EXISTS alert_history_by_last ON alert_history (last_ts);
`

// Options configure the durable store.
type Options struct {
	// Dir is where hm.db lives. An empty dir means an in-memory database,
	// which is what the tests use.
	Dir string
	// BusyTimeout is how long a writer waits on a locked database.
	BusyTimeout time.Duration
	Log         *slog.Logger
}

// Store owns the database handle.
type Store struct {
	db *sql.DB
	// read serves searches, history and downloads on connections of their
	// own, so a slow reader never holds a connection a writer needs.
	read *sql.DB
	path string

	// logDays remembers which log tables exist, keyed "day/kind", so a batch
	// does not ask
	// SQLite to create them again on every write.
	logDaysMu sync.Mutex
	logDays   map[string]bool
	// retiringLogs counts the archives and drops under way per "day/kind".
	retiringLogs map[string]int

	// SQLite wakes waiting writers in no fair order, so concurrent log
	// batches queue here instead of starving each other past busy_timeout.
	logWriteMu sync.Mutex

	reclaimMu  sync.Mutex
	reclaiming atomic.Bool

	// activityAt is when each day's activity was last computed.
	activityMu   sync.Mutex
	activityAt   map[string]time.Time
	activityDays map[string]*activityDay

	stats logStats
}

// Open prepares the database, creating it when needed.
func Open(o Options) (*Store, error) {
	path := "file::memory:?cache=shared"
	if o.Dir != "" {
		path = filepath.Join(o.Dir, "hm.db")
	}
	busy := o.BusyTimeout
	if busy <= 0 {
		busy = 5 * time.Second
	}

	dsn := path + fmt.Sprintf("?_pragma=busy_timeout(%d)", busy.Milliseconds())
	if o.Dir == "" {
		dsn = path + fmt.Sprintf("&_pragma=busy_timeout(%d)", busy.Milliseconds())
	}
	// WAL keeps readers out of the writer's way; NORMAL trades a fsync per
	// transaction for durability only against an OS crash, which is the right
	// trade for aggregates that are rebuilt from the wire anyway.
	dsn += "&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)"
	// The WAL shrinks back after a checkpoint instead of keeping its largest
	// size; 8 MB of page cache per connection, four connections.
	dsn += "&_pragma=journal_size_limit(67108864)&_pragma=cache_size(-8000)"
	// A deferred transaction that reads first cannot wait for the write lock:
	// SQLite fails it with SQLITE_BUSY at once instead of honouring busy_timeout.
	dsn += "&_txlock=immediate"

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	// One writer avoids SQLITE_BUSY between our own connections; reads are
	// short and go through the same pool.
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(0)

	log := o.Log
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	ctx := context.Background()
	fresh, err := isEmpty(ctx, db)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	if fresh {
		// Only takes effect before the first table exists.
		if _, err := db.ExecContext(ctx, `PRAGMA auto_vacuum = INCREMENTAL`); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("store: auto_vacuum: %w", err)
		}
	}
	if _, err := db.ExecContext(ctx, schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: schema: %w", err)
	}
	s := &Store{db: db, read: db, path: path}
	if o.Dir != "" {
		s.switchToIncrementalVacuum(ctx, o.Dir, log)
		read, err := sql.Open("sqlite", path+fmt.Sprintf(
			"?_pragma=busy_timeout(%d)&_pragma=query_only(1)&_pragma=cache_size(-4000)", busy.Milliseconds()))
		if err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("store: open %s for reading: %w", path, err)
		}
		read.SetMaxOpenConns(4)
		read.SetMaxIdleConns(4)
		s.read = read
	}
	return s, nil
}

func isEmpty(ctx context.Context, db *sql.DB) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master`).Scan(&n)
	return n == 0, err
}

func pragmaInt(ctx context.Context, db *sql.DB, name string) (int64, error) {
	var n int64
	err := db.QueryRowContext(ctx, `PRAGMA `+name).Scan(&n)
	return n, err
}

// switchToIncrementalVacuum rewrites a database made before auto_vacuum was
// set, once: SQLite only changes the mode with a full VACUUM. That needs about
// twice the live data in free space, so it waits for a start that has it.
func (s *Store) switchToIncrementalVacuum(ctx context.Context, dir string, log *slog.Logger) {
	mode, err := pragmaInt(ctx, s.db, "auto_vacuum")
	if err != nil || mode == 2 {
		return
	}
	pageSize, err1 := pragmaInt(ctx, s.db, "page_size")
	pages, err2 := pragmaInt(ctx, s.db, "page_count")
	free, err3 := pragmaInt(ctx, s.db, "freelist_count")
	if err1 != nil || err2 != nil || err3 != nil {
		return
	}
	live := (pages - free) * pageSize
	if avail, ok := freeBytes(dir); ok && avail < 2*live {
		log.Warn("database not compacted: not enough free disk space, will try again on the next start",
			"needMB", 2*live>>20, "freeMB", avail>>20)
		return
	}

	before := s.Bytes()
	started := time.Now()
	log.Info("compacting the database once so freed space goes back to the disk; the server starts when it is done",
		"sizeMB", before>>20, "liveMB", live>>20)
	if _, err := s.db.ExecContext(ctx, `PRAGMA auto_vacuum = INCREMENTAL`); err != nil {
		log.Warn("database not compacted", "error", err)
		return
	}
	if _, err := s.db.ExecContext(ctx, `VACUUM`); err != nil {
		log.Warn("database not compacted, will try again on the next start", "error", err)
		return
	}
	_, _ = s.db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`)
	log.Info("database compacted", "beforeMB", before>>20, "afterMB", s.Bytes()>>20,
		"took", time.Since(started).Round(time.Second).String())
}

// Each incremental_vacuum step holds the write lock for about 0.2 s on the
// reference machine; the pause lets waiting writers in, since SQLite does not
// queue them fairly and back-to-back steps starved them past busy_timeout.
const (
	reclaimChunk = 512
	reclaimPause = 250 * time.Millisecond
)

// ReclaimSpace gives the pages freed by deletes and dropped tables back to the
// disk, a chunk at a time. It returns how many bytes the file shrank by. Only
// one runs at a time; a second caller returns straight away.
func (s *Store) ReclaimSpace(ctx context.Context) (int64, error) {
	if !s.reclaimMu.TryLock() {
		return 0, nil
	}
	defer s.reclaimMu.Unlock()
	s.reclaiming.Store(true)
	defer s.reclaiming.Store(false)
	pageSize, err := pragmaInt(ctx, s.db, "page_size")
	if err != nil {
		return 0, err
	}
	var freed int64
	for ctx.Err() == nil {
		free, err := pragmaInt(ctx, s.db, "freelist_count")
		if err != nil {
			return freed, err
		}
		if free == 0 {
			break
		}
		if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`PRAGMA incremental_vacuum(%d)`, reclaimChunk)); err != nil {
			return freed, err
		}
		after, err := pragmaInt(ctx, s.db, "freelist_count")
		if err != nil {
			return freed, err
		}
		if after >= free {
			// auto_vacuum is not incremental yet; nothing can be returned.
			break
		}
		freed += (free - after) * pageSize
		if after == 0 {
			break
		}
		select {
		case <-ctx.Done():
		case <-time.After(reclaimPause):
		}
	}
	if freed > 0 {
		// The file only shrinks when a checkpoint moves the freed pages out of
		// the write-ahead log; an idle monitor would otherwise wait for one.
		if _, err := s.db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
			return freed, err
		}
	}
	return freed, nil
}

// Close releases the database.
func (s *Store) Close() error {
	if s.read != s.db {
		_ = s.read.Close()
	}
	return s.db.Close()
}

// Path is where the database lives, for logs and the backup action.
func (s *Store) Path() string { return s.path }

// Bytes is the database's size on disk, write-ahead log included.
func (s *Store) Bytes() int64 {
	if s.path == "" {
		return 0
	}
	var size int64
	for _, f := range []string{s.path, s.path + "-wal"} {
		if info, err := os.Stat(f); err == nil {
			size += info.Size()
		}
	}
	return size
}

// DB exposes the handle for the few places that need their own statement.
func (s *Store) DB() *sql.DB { return s.db }
