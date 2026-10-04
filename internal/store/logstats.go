package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"
)

// logStats keeps what each log table holds without scanning it: finished
// days measured once and saved in log_table_stats, today's rows counted as
// the writer adds them.
type logStats struct {
	mu sync.Mutex
	// base is a table's rows when it was last counted, and added what the
	// writer has put in since; known says base has been counted.
	base  map[string]int64
	added map[string]int64
	known map[string]bool
	// bytes is a finished table's measured size.
	bytes     map[string]int64
	loaded    bool
	measuring map[string]bool
}

// LogDayStat is what one day of logs holds, across its kinds.
type LogDayStat struct {
	Rows  int64
	Bytes int64
	// Estimated says Bytes is worked out from the rows rather than measured:
	// today's tables are still growing.
	Estimated bool
	// Measuring says a table of the day has not been counted yet; its rows
	// and bytes are missing from the totals.
	Measuring bool
}

func (st *logStats) init() {
	if st.base == nil {
		st.base, st.added, st.known = map[string]int64{}, map[string]int64{}, map[string]bool{}
		st.bytes, st.measuring = map[string]int64{}, map[string]bool{}
	}
}

// wrote records rows the writer committed to a table.
func (st *logStats) wrote(table string, n int) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.init()
	st.added[table] += int64(n)
}

func (st *logStats) forget(table string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.init()
	delete(st.base, table)
	delete(st.added, table)
	delete(st.known, table)
	delete(st.bytes, table)
}

func (s *Store) loadLogStats(ctx context.Context) error {
	s.stats.mu.Lock()
	defer s.stats.mu.Unlock()
	s.stats.init()
	if s.stats.loaded {
		return nil
	}
	rows, err := s.read.QueryContext(ctx, `SELECT name, rows, bytes FROM log_table_stats`)
	if err != nil {
		return fmt.Errorf("store: log stats: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var (
			name        string
			count, size int64
		)
		if err := rows.Scan(&name, &count, &size); err != nil {
			return err
		}
		s.stats.base[name], s.stats.bytes[name], s.stats.known[name] = count, size, true
	}
	if err := rows.Err(); err != nil {
		return err
	}
	s.stats.loaded = true
	return nil
}

// LogDayStats is what each day of logs holds, from what was measured before:
// it never scans a table. A table not measured yet is measured in the
// background and reported as Measuring meanwhile.
func (s *Store) LogDayStats(ctx context.Context, today string) (map[string]LogDayStat, error) {
	if err := s.loadLogStats(ctx); err != nil {
		return nil, err
	}
	tables, err := s.LogTables(ctx)
	if err != nil {
		return nil, err
	}
	s.stats.mu.Lock()
	perRow := map[string]float64{}
	var anyPerRow float64
	for _, t := range tables {
		if t.Day < today && s.stats.saved(t.Name()) && s.stats.base[t.Name()] > 0 {
			r := float64(s.stats.bytes[t.Name()]) / float64(s.stats.base[t.Name()])
			perRow[t.Suffix], anyPerRow = r, r
		}
	}
	out := map[string]LogDayStat{}
	var toMeasure []LogTable
	for _, t := range tables {
		day := out[t.Day]
		name := t.Name()
		finished := t.Day < today
		if !s.stats.known[name] || (finished && !s.stats.saved(name)) {
			day.Measuring = true
			toMeasure = append(toMeasure, t)
			out[t.Day] = day
			continue
		}
		rows := s.stats.base[name] + s.stats.added[name]
		day.Rows += rows
		if finished {
			day.Bytes += s.stats.bytes[name]
		} else {
			r, ok := perRow[t.Suffix]
			if !ok {
				r = anyPerRow
			}
			day.Bytes += int64(r * float64(rows))
			day.Estimated = true
		}
		out[t.Day] = day
	}
	s.stats.mu.Unlock()
	for _, t := range toMeasure {
		s.measureLater(t, t.Day < today)
	}
	return out, nil
}

// MeasureLogTables measures, in the background, every table not measured
// yet: the rollover calls it so a day that has just finished is ready before
// anyone opens the Storage tab.
func (s *Store) MeasureLogTables(ctx context.Context, today string) {
	_, _ = s.LogDayStats(ctx, today)
}

// saved says a table's size has been measured; today's tables are counted
// but not measured, and are measured once their day is over. Callers hold mu.
func (st *logStats) saved(table string) bool {
	_, ok := st.bytes[table]
	return ok
}

func (s *Store) measureLater(t LogTable, finished bool) {
	name := t.Name()
	s.stats.mu.Lock()
	if s.stats.measuring[name] {
		s.stats.mu.Unlock()
		return
	}
	s.stats.measuring[name] = true
	s.stats.mu.Unlock()
	go func() {
		defer func() {
			s.stats.mu.Lock()
			delete(s.stats.measuring, name)
			s.stats.mu.Unlock()
		}()
		_ = s.measure(context.Background(), t, finished)
	}()
}

// measure counts a table's rows and, for a finished day, its bytes, then
// keeps them; the writer's additions from the start of the count on are added
// to the count.
func (s *Store) measure(ctx context.Context, t LogTable, finished bool) error {
	name := t.Name()
	s.stats.mu.Lock()
	s.stats.init()
	s.stats.added[name] = 0
	s.stats.mu.Unlock()

	var rows int64
	if err := s.read.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM %s`, name)).Scan(&rows); err != nil {
		if isMissingTable(err) {
			return nil
		}
		return err
	}
	var size int64
	if finished {
		var err error
		if size, err = s.tableBytes(ctx, t); err != nil {
			return err
		}
		if _, err := s.db.ExecContext(ctx, `INSERT INTO log_table_stats (name, rows, bytes, measured) VALUES (?, ?, ?, ?)
			ON CONFLICT(name) DO UPDATE SET rows = excluded.rows, bytes = excluded.bytes, measured = excluded.measured`,
			name, rows, size, time.Now().UnixMilli()); err != nil {
			return fmt.Errorf("store: save log stats: %w", err)
		}
	}
	s.stats.mu.Lock()
	defer s.stats.mu.Unlock()
	if _, dropped := s.stats.added[name]; !dropped {
		return nil
	}
	s.stats.base[name], s.stats.known[name] = rows, true
	if finished {
		s.stats.bytes[name] = size
	}
	return nil
}

// tableBytes is the pages of one day of one kind: the table, its indexes and
// its search index, each b-tree read on its own rather than the whole file.
func (s *Store) tableBytes(ctx context.Context, t LogTable) (int64, error) {
	rows, err := s.read.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE rootpage > 0
		AND (name = ? OR name LIKE ? ESCAPE '\' OR name LIKE ? ESCAPE '\')`,
		t.Name(), likePrefix(t.Name()+"_"), likePrefix(t.FTSName()+"_"))
	if err != nil {
		return 0, fmt.Errorf("store: log table b-trees: %w", err)
	}
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			_ = rows.Close()
			return 0, err
		}
		names = append(names, n)
	}
	_ = rows.Close()
	var total int64
	for _, n := range names {
		var size sql.NullInt64
		if err := s.read.QueryRowContext(ctx, `SELECT SUM(pgsize) FROM dbstat('main', 1) WHERE name = ?`, n).Scan(&size); err != nil {
			return 0, fmt.Errorf("store: log table size: %w", err)
		}
		total += size.Int64
	}
	return total, nil
}

func likePrefix(prefix string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(prefix) + "%"
}

func (s *Store) forgetLogStats(ctx context.Context, t LogTable) {
	s.stats.forget(t.Name())
	_, _ = s.db.ExecContext(ctx, `DELETE FROM log_table_stats WHERE name = ?`, t.Name())
}
