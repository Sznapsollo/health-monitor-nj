package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// AlertRow is one burst of one alert, as the Alerts tab shows it: repeats
// inside the grouping window are folded into Count rather than added as rows.
// A later burst of the same alert has its own ID, so "it failed at 09:00 and
// again at 17:00" survives the trip to disk.
type AlertRow struct {
	ID            string         `json:"id"`
	Platform      string         `json:"platform"`
	Signal        string         `json:"signal,omitempty"`
	Level         string         `json:"level"`
	Category      string         `json:"category,omitempty"`
	Message       string         `json:"message"`
	GroupKey      string         `json:"groupKey,omitempty"`
	Source        string         `json:"source,omitempty"`
	Target        string         `json:"target,omitempty"`
	Count         int64          `json:"count"`
	First         time.Time      `json:"first"`
	Last          time.Time      `json:"last"`
	Silenced      bool           `json:"silenced,omitempty"`
	SilenceReason string         `json:"silenceReason,omitempty"`
	Data          map[string]any `json:"data,omitempty"`
}

// Day is the partition a row is purged with, taken from when the burst began.
func (r AlertRow) Day() string { return r.First.UTC().Format(logDayLayout) }

// The live store holds the running total for a burst, so the durable row is
// replaced rather than added to: writing the same burst twice cannot
// double-count it, and a write that never made it is repaired by the next
// occurrence.
const alertUpsert = `
INSERT INTO alert_history (id, platform, day, group_key, signal, level, category, message,
                           source, target, count, first_ts, last_ts, silenced, silence_reason, payload)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (id) DO UPDATE SET
    count          = excluded.count,
    last_ts        = excluded.last_ts,
    level          = excluded.level,
    message        = excluded.message,
    silenced       = excluded.silenced,
    silence_reason = excluded.silence_reason,
    payload        = excluded.payload
`

// WriteAlerts stores a batch of alert bursts.
func (s *Store) WriteAlerts(ctx context.Context, rows []AlertRow) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, alertUpsert)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()

	for _, r := range rows {
		payload, err := json.Marshal(r.Data)
		if err != nil {
			payload = []byte("{}")
		}
		_, err = stmt.ExecContext(ctx, r.ID, r.Platform, r.Day(), r.GroupKey, r.Signal,
			r.Level, r.Category, r.Message, r.Source, r.Target, r.Count,
			r.First.UnixMilli(), r.Last.UnixMilli(), r.Silenced, r.SilenceReason, string(payload))
		if err != nil {
			return fmt.Errorf("store: write alert: %w", err)
		}
	}
	return tx.Commit()
}

// AlertQuery is what the Alerts tab asks for when it looks back.
type AlertQuery struct {
	Platform string
	// From and To bound the window. A burst counts as inside it when any part
	// of it was, so an alert running over midnight shows on both days.
	From time.Time
	To   time.Time
	// Levels narrows to ERROR, WARN and so on; empty means every level.
	Levels []string
	// Category narrows to one category.
	Category string
	// Text matches the message, case-insensitively.
	Text string
	// Silenced, when false, leaves out what was suppressed.
	Silenced bool
	Limit    int
}

// SearchAlerts reads the history, newest burst first.
func (s *Store) SearchAlerts(ctx context.Context, q AlertQuery) ([]AlertRow, error) {
	if q.Limit <= 0 {
		q.Limit = 500
	}
	var (
		where []string
		args  []any
	)
	if q.Platform != "" {
		where = append(where, "platform = ?")
		args = append(args, q.Platform)
	}
	if !q.From.IsZero() {
		where = append(where, "last_ts >= ?")
		args = append(args, q.From.UnixMilli())
	}
	if !q.To.IsZero() {
		where = append(where, "first_ts <= ?")
		args = append(args, q.To.UnixMilli())
	}
	if len(q.Levels) > 0 {
		where = append(where, "level IN ("+strings.TrimSuffix(strings.Repeat("?,", len(q.Levels)), ",")+")")
		for _, level := range q.Levels {
			args = append(args, level)
		}
	}
	if q.Category != "" {
		where = append(where, "category = ?")
		args = append(args, q.Category)
	}
	if q.Text != "" {
		where = append(where, "message LIKE ?")
		args = append(args, "%"+q.Text+"%")
	}
	if !q.Silenced {
		where = append(where, "silenced = 0")
	}

	query := `SELECT id, platform, group_key, signal, level, category, message, source, target,
	                 count, first_ts, last_ts, silenced, silence_reason, payload
	          FROM alert_history`
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY last_ts DESC LIMIT ?"
	args = append(args, q.Limit)

	rows, err := s.read.QueryContext(ctx, query, args...)
	if err != nil {
		if isMissingTable(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("store: search alerts: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []AlertRow
	for rows.Next() {
		r, err := scanAlertRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func scanAlertRow(rows *sql.Rows) (AlertRow, error) {
	var (
		r                 AlertRow
		first, last       int64
		payload           sql.NullString
		group, signal     sql.NullString
		category, source  sql.NullString
		target, silReason sql.NullString
	)
	err := rows.Scan(&r.ID, &r.Platform, &group, &signal, &r.Level, &category, &r.Message,
		&source, &target, &r.Count, &first, &last, &r.Silenced, &silReason, &payload)
	if err != nil {
		return AlertRow{}, fmt.Errorf("store: scan alert: %w", err)
	}
	r.GroupKey, r.Signal, r.Category = group.String, signal.String, category.String
	r.Source, r.Target, r.SilenceReason = source.String, target.String, silReason.String
	r.First, r.Last = time.UnixMilli(first), time.UnixMilli(last)
	if payload.String != "" {
		_ = json.Unmarshal([]byte(payload.String), &r.Data)
	}
	return r, nil
}

// AlertHistoryDays lists every day the alert history holds, oldest first.
func (s *Store) AlertHistoryDays(ctx context.Context) ([]string, error) {
	days, err := s.AlertDays(ctx, "")
	for i, j := 0, len(days)-1; i < j; i, j = i+1, j-1 {
		days[i], days[j] = days[j], days[i]
	}
	return days, err
}

// AlertDays lists the days the history holds, newest first, for a day picker.
func (s *Store) AlertDays(ctx context.Context, platform string) ([]string, error) {
	rows, err := s.read.QueryContext(ctx,
		`SELECT DISTINCT day FROM alert_history WHERE (? = '' OR platform = ?) ORDER BY day DESC`,
		platform, platform)
	if err != nil {
		if isMissingTable(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("store: list alert days: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []string
	for rows.Next() {
		var day string
		if err := rows.Scan(&day); err != nil {
			return nil, err
		}
		out = append(out, day)
	}
	return out, rows.Err()
}

// PurgeAlerts drops history older than the cutoff day. A day column rather
// than a timestamp keeps this a plain indexed delete.
func (s *Store) PurgeAlerts(ctx context.Context, before string) (int64, error) {
	if err := validDay(before); err != nil {
		return 0, err
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM alert_history WHERE day < ?`, before)
	if err != nil {
		if isMissingTable(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("store: purge alerts: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// AlertDay is one day of the alert history and how many bursts it holds.
type AlertDay struct {
	Day    string `json:"day"`
	Alerts int64  `json:"alerts"`
}

// AlertDayCounts lists the days the alert history holds, newest first.
func (s *Store) AlertDayCounts(ctx context.Context) ([]AlertDay, error) {
	rows, err := s.read.QueryContext(ctx,
		`SELECT day, COUNT(*) FROM alert_history GROUP BY day ORDER BY day DESC`)
	if err != nil {
		if isMissingTable(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("store: count alert days: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []AlertDay
	for rows.Next() {
		var d AlertDay
		if err := rows.Scan(&d.Day, &d.Alerts); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DeleteAlertDay removes one day of the alert history without archiving it.
func (s *Store) DeleteAlertDay(ctx context.Context, day string) (int64, error) {
	if err := validDay(day); err != nil {
		return 0, err
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM alert_history WHERE day = ?`, day)
	if err != nil {
		return 0, fmt.Errorf("store: delete alert day: %w", err)
	}
	return res.RowsAffected()
}
