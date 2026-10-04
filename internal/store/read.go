package store

import (
	"context"
	"fmt"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/state"
)

const aggColumns = `count, sum_ms, min_ms, max_ms, samples`

// Restore reads a signal's recent minutes back into memory so a restart does
// not leave the charts blank. Totals cover the totals tier; values and the
// kept pairs only the detail tier, which is all memory holds of them anyway.
type Restore struct {
	Platform string
	Signal   string
	// DetailFrom and TotalsFrom are epoch minutes.
	DetailFrom int64
	TotalsFrom int64
	To         int64
	// Pairs are the pairs to read back; others are left on disk.
	Pairs []state.Pair
}

// Load returns the stored minutes of the window, oldest first.
func (s *Store) Load(ctx context.Context, r Restore) ([]state.RestoredMinute, error) {
	byMinute := map[int64]*state.RestoredMinute{}
	var order []int64
	get := func(minute int64) *state.RestoredMinute {
		m, ok := byMinute[minute]
		if !ok {
			m = &state.RestoredMinute{Minute: minute}
			byMinute[minute] = m
			order = append(order, minute)
		}
		return m
	}

	rows, err := s.read.QueryContext(ctx, `SELECT minute, `+aggColumns+` FROM agg_total
WHERE platform = ? AND signal = ? AND minute >= ? AND minute <= ? ORDER BY minute`,
		r.Platform, r.Signal, r.TotalsFrom, r.To)
	if err != nil {
		return nil, fmt.Errorf("store: load totals: %w", err)
	}
	for rows.Next() {
		var minute int64
		var a state.Agg
		if err := rows.Scan(&minute, &a.Count, &a.SumMS, &a.MinMS, &a.MaxMS, &a.Samples); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("store: scan totals: %w", err)
		}
		get(minute).Total = a
	}
	if err := closeRows(rows); err != nil {
		return nil, err
	}

	rows, err = s.read.QueryContext(ctx, `SELECT minute, dim, value, `+aggColumns+` FROM agg_dim
WHERE platform = ? AND signal = ? AND minute >= ? AND minute <= ?`,
		r.Platform, r.Signal, r.DetailFrom, r.To)
	if err != nil {
		return nil, fmt.Errorf("store: load values: %w", err)
	}
	for rows.Next() {
		var minute int64
		var dim, value string
		var a state.Agg
		if err := rows.Scan(&minute, &dim, &value, &a.Count, &a.SumMS, &a.MinMS, &a.MaxMS, &a.Samples); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("store: scan values: %w", err)
		}
		m := get(minute)
		if m.Dims == nil {
			m.Dims = map[string]map[string]state.Agg{}
		}
		if m.Dims[dim] == nil {
			m.Dims[dim] = map[string]state.Agg{}
		}
		m.Dims[dim][value] = a
	}
	if err := closeRows(rows); err != nil {
		return nil, err
	}

	for _, p := range r.Pairs {
		rows, err := s.read.QueryContext(ctx, `SELECT minute, value, sub_value, `+aggColumns+` FROM agg_pair
WHERE platform = ? AND signal = ? AND minute >= ? AND minute <= ? AND dim = ? AND sub = ?`,
			r.Platform, r.Signal, r.DetailFrom, r.To, p.Main, p.Sub)
		if err != nil {
			return nil, fmt.Errorf("store: load pairs: %w", err)
		}
		for rows.Next() {
			var minute int64
			var value, subValue string
			var a state.Agg
			if err := rows.Scan(&minute, &value, &subValue, &a.Count, &a.SumMS, &a.MinMS, &a.MaxMS, &a.Samples); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("store: scan pairs: %w", err)
			}
			m := get(minute)
			if m.Pairs == nil {
				m.Pairs = map[state.Pair]map[string]map[string]state.Agg{}
			}
			if m.Pairs[p] == nil {
				m.Pairs[p] = map[string]map[string]state.Agg{}
			}
			if m.Pairs[p][value] == nil {
				m.Pairs[p][value] = map[string]state.Agg{}
			}
			m.Pairs[p][value][subValue] = a
		}
		if err := closeRows(rows); err != nil {
			return nil, err
		}
	}

	out := make([]state.RestoredMinute, 0, len(order))
	for _, minute := range order {
		out = append(out, *byMinute[minute])
	}
	return out, nil
}

func closeRows(rows interface {
	Err() error
	Close() error
}) error {
	err := rows.Err()
	if cerr := rows.Close(); err == nil {
		err = cerr
	}
	return err
}

// Query asks for a window that the hot tiers cannot answer.
type Query struct {
	Platform string
	Signal   string
	From     int64
	To       int64
	// Group is the dimension to break down by; empty returns totals only.
	Group string
}

// Row is one minute of one dimension value (or of the total). Past a
// signal's detail_days a row stands for a whole hour, keyed by its first
// minute.
type Row struct {
	Minute int64
	Value  string
	Agg    state.Agg
}

// Series answers a window from disk. It is what /api/series falls back to
// once a chart reaches past the hot tiers.
func (s *Store) Series(ctx context.Context, q Query) ([]Row, error) {
	if q.Group == "" {
		return s.scanRows(ctx, `SELECT minute, '', `+aggColumns+` FROM agg_total
WHERE platform = ? AND signal = ? AND minute >= ? AND minute <= ? ORDER BY minute`,
			q.Platform, q.Signal, q.From, q.To)
	}
	return s.scanRows(ctx, `SELECT minute, value, `+aggColumns+` FROM (
    SELECT minute, value, `+aggColumns+` FROM agg_dim_hour
    WHERE platform = ? AND signal = ? AND dim = ? AND minute >= ? AND minute <= ?
    UNION ALL
    SELECT minute, value, `+aggColumns+` FROM agg_dim
    WHERE platform = ? AND signal = ? AND dim = ? AND minute >= ? AND minute <= ?
) ORDER BY minute`,
		q.Platform, q.Signal, q.Group, q.From-59, q.To,
		q.Platform, q.Signal, q.Group, q.From, q.To)
}

func (s *Store) scanRows(ctx context.Context, query string, args ...any) ([]Row, error) {
	rows, err := s.read.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: series: %w", err)
	}
	var out []Row
	for rows.Next() {
		var r Row
		if err := rows.Scan(&r.Minute, &r.Value, &r.Agg.Count, &r.Agg.SumMS, &r.Agg.MinMS, &r.Agg.MaxMS, &r.Agg.Samples); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("store: scan series: %w", err)
		}
		out = append(out, r)
	}
	return out, closeRows(rows)
}

// Purge drops everything of one signal older than the cutoff, which is how
// its durable retention is applied.
func (s *Store) Purge(ctx context.Context, platform, signal string, before int64) (int64, error) {
	var n int64
	for _, table := range []string{"agg_total", "agg_dim", "agg_pair", "agg_dim_hour", "agg_pair_hour", "dim_label"} {
		column := "minute"
		if table == "dim_label" {
			column = "seen"
		}
		res, err := s.db.ExecContext(ctx,
			`DELETE FROM `+table+` WHERE platform = ? AND signal = ? AND `+column+` < ?`, platform, signal, before)
		if err != nil {
			return n, fmt.Errorf("store: purge %s: %w", table, err)
		}
		if affected, err := res.RowsAffected(); err == nil {
			n += affected
		}
	}
	return n, nil
}

// Labels returns the stored names of a signal's labelled values.
func (s *Store) Labels(ctx context.Context, platform, signal string) ([]state.Label, error) {
	rows, err := s.read.QueryContext(ctx,
		`SELECT dim, value, name FROM dim_label WHERE platform = ? AND signal = ?`, platform, signal)
	if err != nil {
		return nil, fmt.Errorf("store: load labels: %w", err)
	}
	var out []state.Label
	for rows.Next() {
		var l state.Label
		if err := rows.Scan(&l.Dim, &l.Value, &l.Name); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("store: scan labels: %w", err)
		}
		out = append(out, l)
	}
	return out, closeRows(rows)
}

// StoredSignal is one platform's signal that has aggregates on disk.
type StoredSignal struct {
	Platform string
	Signal   string
}

// StoredSignals lists every signal with aggregates on disk, including ones no
// catalogue declares any more.
func (s *Store) StoredSignals(ctx context.Context) ([]StoredSignal, error) {
	rows, err := s.read.QueryContext(ctx, `SELECT DISTINCT platform, signal FROM agg_total`)
	if err != nil {
		return nil, fmt.Errorf("store: stored signals: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []StoredSignal
	for rows.Next() {
		var st StoredSignal
		if err := rows.Scan(&st.Platform, &st.Signal); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// Rollup sums the value and pair rows of whole hours before the cutoff into
// the hourly tables and removes them from the per-minute ones.
func (s *Store) Rollup(ctx context.Context, platform, signal string, before int64) (int64, error) {
	before -= before % 60
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	sums := `SUM(count), SUM(sum_ms),
       COALESCE(MIN(CASE WHEN samples > 0 THEN min_ms END), 0),
       COALESCE(MAX(CASE WHEN samples > 0 THEN max_ms END), 0), SUM(samples)`
	steps := []string{
		`INSERT INTO agg_dim_hour (platform, signal, minute, dim, value, ` + aggColumns + `)
SELECT platform, signal, minute - minute % 60 AS hour, dim, value, ` + sums + `
FROM agg_dim WHERE platform = ? AND signal = ? AND minute < ?
GROUP BY hour, dim, value
ON CONFLICT (platform, signal, minute, dim, value) DO UPDATE SET` + accumulate,
		`INSERT INTO agg_pair_hour (platform, signal, minute, dim, value, sub, sub_value, ` + aggColumns + `)
SELECT platform, signal, minute - minute % 60 AS hour, dim, value, sub, sub_value, ` + sums + `
FROM agg_pair WHERE platform = ? AND signal = ? AND minute < ?
GROUP BY hour, dim, sub, value, sub_value
ON CONFLICT (platform, signal, minute, dim, sub, value, sub_value) DO UPDATE SET` + accumulate,
	}
	for _, q := range steps {
		if _, err := tx.ExecContext(ctx, q, platform, signal, before); err != nil {
			return 0, fmt.Errorf("store: roll up: %w", err)
		}
	}
	var n int64
	for _, table := range []string{"agg_dim", "agg_pair"} {
		res, err := tx.ExecContext(ctx,
			`DELETE FROM `+table+` WHERE platform = ? AND signal = ? AND minute < ?`, platform, signal, before)
		if err != nil {
			return 0, fmt.Errorf("store: roll up %s: %w", table, err)
		}
		if affected, err := res.RowsAffected(); err == nil {
			n += affected
		}
	}
	return n, tx.Commit()
}

// Oldest is the earliest minute held for a signal, or zero when there is none.
func (s *Store) Oldest(ctx context.Context, platform, signal string) (int64, error) {
	var minute *int64
	err := s.read.QueryRowContext(ctx,
		`SELECT MIN(minute) FROM agg_total WHERE platform = ? AND signal = ?`,
		platform, signal).Scan(&minute)
	if err != nil {
		return 0, err
	}
	if minute == nil {
		return 0, nil
	}
	return *minute, nil
}

// Counts reports how many rows each signal holds across the chart tables,
// for the state endpoint.
func (s *Store) Counts(ctx context.Context) (map[string]int64, error) {
	out := map[string]int64{}
	for _, table := range []string{"agg_total", "agg_dim", "agg_pair", "agg_dim_hour", "agg_pair_hour"} {
		rows, err := s.read.QueryContext(ctx,
			`SELECT platform, signal, COUNT(*) FROM `+table+` GROUP BY platform, signal`)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var platform, signal string
			var n int64
			if err := rows.Scan(&platform, &signal, &n); err != nil {
				_ = rows.Close()
				return nil, err
			}
			out[platform+"/"+signal] += n
		}
		if err := closeRows(rows); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// MinuteOf is re-exported so callers do not need the state package just to
// work out a cutoff.
func MinuteOf(t time.Time) int64 { return state.MinuteOf(t) }

// RawRow is one stored minute of one value, or of one value pair, with the
// dimensions it stands for: what the rules preview replays.
type RawRow struct {
	Minute int64
	Dims   map[string]string
	Agg    state.Agg
}

// Rows returns, for a window, the per-minute rows of one dimension and of
// every stored pair that includes it. It is meant for previewing rules over
// a recent window, not for charting.
func (s *Store) Rows(ctx context.Context, platform, signal, dim string, from, to int64, limit int) ([]RawRow, error) {
	if limit <= 0 {
		limit = 50000
	}
	rows, err := s.read.QueryContext(ctx, `
SELECT minute, dim, value, '', '', `+aggColumns+` FROM agg_dim
WHERE platform = ? AND signal = ? AND minute >= ? AND minute <= ? AND dim = ?
UNION ALL
SELECT minute, dim, value, sub, sub_value, `+aggColumns+` FROM agg_pair
WHERE platform = ? AND signal = ? AND minute >= ? AND minute <= ? AND (dim = ? OR sub = ?)
LIMIT ?`,
		platform, signal, from, to, dim, platform, signal, from, to, dim, dim, limit)
	if err != nil {
		return nil, fmt.Errorf("store: rows: %w", err)
	}
	var out []RawRow
	for rows.Next() {
		var r RawRow
		var d, value, sub, subValue string
		if err := rows.Scan(&r.Minute, &d, &value, &sub, &subValue,
			&r.Agg.Count, &r.Agg.SumMS, &r.Agg.MinMS, &r.Agg.MaxMS, &r.Agg.Samples); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("store: scan rows: %w", err)
		}
		r.Dims = map[string]string{d: value}
		if sub != "" {
			r.Dims[sub] = subValue
		}
		out = append(out, r)
	}
	return out, closeRows(rows)
}

// Signals lists the signals a platform has rows for.
func (s *Store) Signals(ctx context.Context, platform string) ([]string, error) {
	rows, err := s.read.QueryContext(ctx,
		`SELECT DISTINCT signal FROM agg_total WHERE platform = ? ORDER BY signal`, platform)
	if err != nil {
		return nil, err
	}
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			_ = rows.Close()
			return nil, err
		}
		out = append(out, name)
	}
	return out, closeRows(rows)
}
