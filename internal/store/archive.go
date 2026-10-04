package store

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// logArchiveName and alertArchiveName are the only shapes an archive file can
// have; a name from a request is matched against them before it reaches the
// filesystem.
var (
	logArchiveName   = regexp.MustCompile(`^logs-(\d{8})-([A-Za-z][A-Za-z0-9_]{0,48})\.ndjson\.gz$`)
	alertArchiveName = regexp.MustCompile(`^alerts-(\d{8})\.ndjson\.gz$`)
)

// AlertsKind is the kind an alert archive is listed under.
const AlertsKind = "alerts"

// ErrArchiveMoved means rows arrived while a day was being archived; the rows
// are kept and the next attempt takes them too.
var ErrArchiveMoved = errors.New("store: rows arrived while archiving")

// ArchiveFile is one day moved out of the database as gzipped JSON lines: one
// kind of log, one LogRow per line, or the alerts, one AlertRow per line.
type ArchiveFile struct {
	File  string `json:"file"`
	Day   string `json:"day"`
	Kind  string `json:"kind"`
	Bytes int64  `json:"bytes"`
	// Written is when the file was last written, which is what its keep
	// period counts from.
	Written time.Time `json:"written"`
	// Alerts marks an alert archive rather than a log one.
	Alerts bool `json:"alerts,omitempty"`
}

func archiveFileName(t LogTable) string { return "logs-" + t.Day + "-" + t.Suffix + ".ndjson.gz" }

func alertArchiveFileName(day string) string { return "alerts-" + day + ".ndjson.gz" }

// ArchiveLogTable writes a table to dir and drops it once the file is known to
// hold every row. A file already there for the same day and kind (rows that
// arrived late, after an earlier archive) is carried over into the new one.
func (s *Store) ArchiveLogTable(ctx context.Context, t LogTable, dir string) (ArchiveFile, error) {
	if err := validDay(t.Day); err != nil {
		return ArchiveFile{}, err
	}
	if err := validSuffix(t.Suffix); err != nil {
		return ArchiveFile{}, err
	}
	defer s.retireLogTable(t)()
	name := archiveFileName(t)
	bytes, err := writeArchive(dir, name, func(emit func(any) error) (int64, error) {
		var rows int64
		err := s.forEachLogTable(ctx, t, LogQuery{}, func(r LogRow) error {
			rows++
			return emit(r)
		})
		if err != nil {
			return 0, err
		}
		var now int64
		if err := s.read.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+t.Name()).Scan(&now); err != nil {
			return 0, fmt.Errorf("store: archive: count %s: %w", t.Name(), err)
		}
		if now != rows {
			return 0, ErrArchiveMoved
		}
		return rows, nil
	})
	if err != nil {
		return ArchiveFile{}, err
	}
	if err := s.dropLogTable(ctx, t); err != nil {
		return ArchiveFile{}, err
	}
	return ArchiveFile{File: name, Day: t.Day, Kind: t.Suffix, Bytes: bytes}, nil
}

// ArchiveAlertDay writes a day of the alert history to dir, every platform in
// one file, and deletes those rows once the file is known to hold them all.
func (s *Store) ArchiveAlertDay(ctx context.Context, day, dir string) (ArchiveFile, error) {
	if err := validDay(day); err != nil {
		return ArchiveFile{}, err
	}
	name := alertArchiveFileName(day)
	var ids []string
	bytes, err := writeArchive(dir, name, func(emit func(any) error) (int64, error) {
		rows, err := s.read.QueryContext(ctx, `SELECT id, platform, group_key, signal, level, category, message,
			source, target, count, first_ts, last_ts, silenced, silence_reason, payload
			FROM alert_history WHERE day = ? ORDER BY first_ts`, day)
		if err != nil {
			return 0, fmt.Errorf("store: archive alerts: %w", err)
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			r, err := scanAlertRow(rows)
			if err != nil {
				return 0, err
			}
			ids = append(ids, r.ID)
			if err := emit(r); err != nil {
				return 0, err
			}
		}
		return int64(len(ids)), rows.Err()
	})
	if err != nil {
		return ArchiveFile{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ArchiveFile{}, err
	}
	defer func() { _ = tx.Rollback() }()
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `DELETE FROM alert_history WHERE id = ?`, id); err != nil {
			return ArchiveFile{}, fmt.Errorf("store: archive alerts: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return ArchiveFile{}, err
	}
	return ArchiveFile{File: name, Day: day, Kind: AlertsKind, Bytes: bytes, Alerts: true}, nil
}

// writeArchive writes dir/name through a partial file: what the file already
// holds, then every row fill emits, as JSON lines. It is renamed into place
// only once it reads back with every line, and returns its size.
func writeArchive(dir, name string, fill func(emit func(any) error) (int64, error)) (int64, error) {
	// Readable by anyone: the folder is meant to be copied out by another job.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, fmt.Errorf("store: archive dir: %w", err)
	}
	path := filepath.Join(dir, name)
	partial := path + ".partial"
	lines, err := writePartial(path, partial, fill)
	if err != nil {
		_ = os.Remove(partial)
		return 0, err
	}
	if got, err := countArchiveLines(partial); err != nil || got != lines {
		_ = os.Remove(partial)
		if err == nil {
			err = fmt.Errorf("store: archive %s holds %d rows, wrote %d", name, got, lines)
		}
		return 0, err
	}
	if err := os.Rename(partial, path); err != nil {
		_ = os.Remove(partial)
		return 0, fmt.Errorf("store: archive %s: %w", name, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

func writePartial(path, partial string, fill func(emit func(any) error) (int64, error)) (int64, error) {
	f, err := os.OpenFile(partial, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, fmt.Errorf("store: archive: %w", err)
	}
	defer func() { _ = f.Close() }()
	buf := bufio.NewWriterSize(f, 1<<20)
	gz := gzip.NewWriter(buf)

	var lines int64
	if prior, err := os.Open(path); err == nil {
		n, err := copyArchive(gz, prior)
		_ = prior.Close()
		if err != nil {
			return 0, fmt.Errorf("store: archive: reading %s: %w", filepath.Base(path), err)
		}
		lines = n
	} else if !os.IsNotExist(err) {
		return 0, err
	}

	rows, err := fill(func(v any) error {
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		_, err = gz.Write(append(b, '\n'))
		return err
	})
	if err != nil {
		return 0, err
	}
	if err := gz.Close(); err != nil {
		return 0, err
	}
	if err := buf.Flush(); err != nil {
		return 0, err
	}
	if err := f.Sync(); err != nil {
		return 0, err
	}
	return lines + rows, nil
}

func copyArchive(dst io.Writer, src io.Reader) (int64, error) {
	zr, err := gzip.NewReader(src)
	if err != nil {
		return 0, err
	}
	defer func() { _ = zr.Close() }()
	var lines int64
	buf := make([]byte, 256<<10)
	for {
		n, err := zr.Read(buf)
		if n > 0 {
			lines += int64(bytes.Count(buf[:n], []byte{'\n'}))
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return lines, werr
			}
		}
		if errors.Is(err, io.EOF) {
			return lines, nil
		}
		if err != nil {
			return lines, err
		}
	}
}

func countArchiveLines(path string) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()
	return copyArchive(io.Discard, f)
}

// Archives lists the archive files in dir, newest day first. A dir that does
// not exist yet holds none.
func Archives(dir string) ([]ArchiveFile, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []ArchiveFile
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		f, ok := parseArchiveName(e.Name())
		if !ok {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		f.Bytes, f.Written = info.Size(), info.ModTime()
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Day != out[j].Day {
			return out[i].Day > out[j].Day
		}
		return out[i].Kind < out[j].Kind
	})
	return out, nil
}

func parseArchiveName(name string) (ArchiveFile, bool) {
	if m := logArchiveName.FindStringSubmatch(name); m != nil {
		return ArchiveFile{File: name, Day: m[1], Kind: m[2]}, true
	}
	if m := alertArchiveName.FindStringSubmatch(name); m != nil {
		return ArchiveFile{File: name, Day: m[1], Kind: AlertsKind, Alerts: true}, true
	}
	return ArchiveFile{}, false
}

// ArchivePath is where a named archive lives, or false for a name that is not
// an archive's.
func ArchivePath(dir, file string) (string, bool) {
	if _, ok := parseArchiveName(file); !ok {
		return "", false
	}
	return filepath.Join(dir, file), true
}

// PurgeArchives removes the files written longer ago than the days keepFor
// gives them; 0 keeps a file.
func PurgeArchives(dir string, now time.Time, keepFor func(ArchiveFile) int) ([]ArchiveFile, error) {
	files, err := Archives(dir)
	if err != nil {
		return nil, err
	}
	var removed []ArchiveFile
	for _, f := range files {
		days := keepFor(f)
		if days <= 0 || now.Before(f.Written.AddDate(0, 0, days)) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, f.File)); err != nil && !os.IsNotExist(err) {
			return removed, err
		}
		removed = append(removed, f)
	}
	return removed, nil
}

// LogDayBytes is how much of the database each day of logs takes: its tables,
// their indexes and their search index.
func (s *Store) LogDayBytes(ctx context.Context) (map[string]int64, error) {
	rows, err := s.read.QueryContext(ctx,
		`SELECT name, SUM(pgsize) FROM dbstat('main', 1) WHERE name LIKE 'log\_%' ESCAPE '\' GROUP BY name`)
	if err != nil {
		return nil, fmt.Errorf("store: log sizes: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]int64{}
	for rows.Next() {
		var name string
		var n int64
		if err := rows.Scan(&name, &n); err != nil {
			return nil, err
		}
		rest := strings.TrimPrefix(strings.TrimPrefix(name, "log_"), "fts_")
		if len(rest) < 8 || validDay(rest[:8]) != nil {
			continue
		}
		out[rest[:8]] += n
	}
	return out, rows.Err()
}

// FreeBytes is the space inside the file that deletes have freed and that
// ReclaimSpace has not yet given back to the disk.
func (s *Store) FreeBytes(ctx context.Context) (int64, error) {
	pageSize, err := pragmaInt(ctx, s.db, "page_size")
	if err != nil {
		return 0, err
	}
	free, err := pragmaInt(ctx, s.db, "freelist_count")
	return free * pageSize, err
}

// Reclaiming reports whether ReclaimSpace is running.
func (s *Store) Reclaiming() bool { return s.reclaiming.Load() }
