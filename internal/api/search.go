package api

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/core"
	"github.com/Sznapsollo/health-monitor-nj/internal/store"
)

// backupName is the only shape a backup file can have. Nothing else is listed,
// and nothing else can be asked for: the name reaches the filesystem, so it is
// matched against this rather than cleaned and hoped about.
var backupName = regexp.MustCompile(`^hm-backup-\d{8}-\d{6}(-\d+)?\.db$`)

// handleSearch is the old "Wyszukiwarka": free text over the stored rows,
// narrowed by kind, account, user and a window — and, unlike the old one,
// able to answer for previous days.
func (d Deps) handleSearch(w http.ResponseWriter, r *http.Request) {
	if d.DB == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "no log store"})
		return
	}
	q := r.URL.Query()
	query := store.LogQuery{
		Platform: d.platformOf(r),
		Signal:   q.Get("kind"),
		Text:     q.Get("text"),
		Account:  q.Get("account"),
		User:     q.Get("user"),
		Limit:    atoi(q.Get("limit")),
	}
	if days := atoi(q.Get("days")); days > 0 {
		query.From = time.Now().AddDate(0, 0, -(days - 1)).Truncate(24 * time.Hour)
	}
	if from := q.Get("from"); from != "" {
		if t, err := time.Parse(time.RFC3339, from); err == nil {
			query.From = t
		}
	}
	if to := q.Get("to"); to != "" {
		if t, err := time.Parse(time.RFC3339, to); err == nil {
			query.To = t
		}
	}

	rows, err := d.DB.SearchLogs(r.Context(), query)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if strings.EqualFold(q.Get("format"), "csv") {
		writeLogsCSV(w, rows)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"platform": query.Platform,
		"rows":     rows,
		"count":    len(rows),
		"kinds":    d.logKinds(r),
	})
}

// logKinds are the kinds the search panel offers, taken from what the platform
// actually declares rather than from a list baked into the UI.
func (d Deps) logKinds(r *http.Request) []string {
	seen := map[string]bool{}
	if d.Registry != nil {
		for _, def := range d.Registry.Definitions(d.platformOf(r)) {
			if def.Kind == "log" {
				seen[def.Name] = true
			}
		}
	}
	for _, name := range builtInLogKinds {
		seen[name] = true
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func (d Deps) diskReadings() []core.DiskReading {
	if d.Core == nil {
		return []core.DiskReading{}
	}
	return d.Core.DiskReadings()
}

// builtInLogKinds are the kinds the legacy adapter itself produces.
var builtInLogKinds = []string{"dailyLogs", "sendLogs"}

var logsCSVHeader = []string{"ts", "signal", "key", "account", "user", "url", "level", "payload"}

func logsCSVRecord(r store.LogRow) []string {
	payload, _ := json.Marshal(r.Payload)
	return []string{
		r.TS.Format(time.RFC3339), r.Signal, r.Key, r.Account, r.User, r.URL, r.Level, string(payload),
	}
}

func writeLogsCSV(w http.ResponseWriter, rows []store.LogRow) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="logs.csv"`)

	c := csv.NewWriter(w)
	defer c.Flush()
	_ = c.Write(logsCSVHeader)
	for _, r := range rows {
		_ = c.Write(logsCSVRecord(r))
	}
}

// handleHistory lists the days that hold logs with their sizes, the archive
// files, and how much of the database is free space not yet given back.
func (d Deps) handleHistory(w http.ResponseWriter, r *http.Request) {
	if d.DB == nil {
		writeJSON(w, http.StatusOK, map[string]any{"days": []any{}, "archives": []any{}})
		return
	}
	today := time.Now().UTC().Format("20060102")
	stats, err := d.DB.LogDayStats(r.Context(), today)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	kindsOf := map[string][]string{}
	if tables, err := d.DB.LogTables(r.Context()); err == nil {
		for _, t := range tables {
			kindsOf[t.Day] = append(kindsOf[t.Day], t.Signal)
		}
	}
	type dayInfo struct {
		Day       string         `json:"day"`
		Rows      int64          `json:"rows"`
		Bytes     int64          `json:"bytes"`
		Estimated bool           `json:"estimated,omitempty"`
		Measuring bool           `json:"measuring,omitempty"`
		Today     bool           `json:"today,omitempty"`
		Leaves    []core.Leaving `json:"leaves,omitempty"`
	}
	out := make([]dayInfo, 0, len(stats))
	for day, st := range stats {
		info := dayInfo{Day: day, Rows: st.Rows, Bytes: st.Bytes, Estimated: st.Estimated, Measuring: st.Measuring, Today: day >= today}
		if d.Core != nil {
			kinds := kindsOf[day]
			sort.Strings(kinds)
			info.Leaves = d.Core.LogDayLeaves(day, kinds)
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Day > out[j].Day })

	archives := []store.ArchiveFile{}
	var archiveBytes int64
	if dir := d.archiveDir(); dir != "" {
		list, err := store.Archives(dir)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		for _, f := range list {
			archiveBytes += f.Bytes
		}
		archives = append(archives, list...)
	}
	type archiveInfo struct {
		store.ArchiveFile
		Name     string     `json:"name,omitempty"`
		DeleteOn *time.Time `json:"deleteOn,omitempty"`
	}
	var names map[string]string
	if d.Core != nil {
		names = d.Core.LogNames()
	}
	files := make([]archiveInfo, 0, len(archives))
	for _, f := range archives {
		info := archiveInfo{ArchiveFile: f}
		if !f.Alerts {
			info.Name = names[f.Kind]
		}
		if d.Core != nil {
			if on, ok := d.Core.FileDeleteOn(f); ok {
				info.DeleteOn = &on
			}
		}
		files = append(files, info)
	}
	type alertDayInfo struct {
		store.AlertDay
		Today   bool       `json:"today,omitempty"`
		Leaves  *time.Time `json:"leaves,omitempty"`
		Archive bool       `json:"archive,omitempty"`
	}
	alertDays := []alertDayInfo{}
	if list, err := d.DB.AlertDayCounts(r.Context()); err == nil {
		for _, a := range list {
			info := alertDayInfo{AlertDay: a, Today: a.Day >= today}
			if d.Core != nil {
				on, archive := d.Core.AlertDayLeaves(a.Day)
				info.Leaves, info.Archive = &on, archive
			}
			alertDays = append(alertDays, info)
		}
	}
	free, _ := d.DB.FreeBytes(r.Context())
	body := map[string]any{
		"days":         out,
		"archives":     files,
		"alertDays":    alertDays,
		"archiving":    d.archiveDir() != "",
		"dbBytes":      d.DB.Bytes(),
		"freeBytes":    free,
		"archiveBytes": archiveBytes,
		"reclaiming":   d.DB.Reclaiming(),
		"disks":        d.diskReadings(),
	}
	if d.Core != nil {
		fallback, byKind := d.Core.LogRetention(builtInLogKinds)
		body["retention"] = map[string]any{"default": fallback, "kinds": byKind, "alerts": d.Core.AlertRetention()}
	}
	writeJSON(w, http.StatusOK, body)
}

// handleHistoryDay streams one day's rows as JSON or CSV, replacing the old
// "download this log file" action.
func (d Deps) handleHistoryDay(w http.ResponseWriter, r *http.Request) {
	if d.DB == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "no log store"})
		return
	}
	day := r.PathValue("day")
	at, err := time.Parse("20060102", day)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "not a day"})
		return
	}

	query := store.LogQuery{
		Platform: d.platformOf(r),
		Signal:   r.URL.Query().Get("kind"),
		From:     at,
		To:       at.Add(24 * time.Hour),
	}

	if strings.EqualFold(r.URL.Query().Get("format"), "csv") {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="logs-%s.csv"`, day))
		c := csv.NewWriter(w)
		_ = c.Write(logsCSVHeader)
		n := 0
		err := d.DB.ForEachLog(r.Context(), query, func(row store.LogRow) error {
			if err := c.Write(logsCSVRecord(row)); err != nil {
				return err
			}
			if n++; n%1000 == 0 {
				c.Flush()
			}
			return c.Error()
		})
		c.Flush()
		if err != nil {
			d.Log.Error("day download cut short", "day", day, "rows", n, "error", err)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="logs-%s.json"`, day))
	_, _ = fmt.Fprintf(w, `{"day":%q,"rows":[`, day)
	n := 0
	err = d.DB.ForEachLog(r.Context(), query, func(row store.LogRow) error {
		b, err := json.Marshal(row)
		if err != nil {
			return err
		}
		if n > 0 {
			if _, err := w.Write([]byte(",")); err != nil {
				return err
			}
		}
		n++
		_, err = w.Write(b)
		return err
	})
	if err != nil {
		d.Log.Error("day download cut short", "day", day, "rows", n, "error", err)
	}
	_, _ = fmt.Fprintf(w, `],"count":%d}`, n)
}

// handleActivity is the old "user stats": minutes of activity per person per
// day, computed by the rollover from the same log rows.
// handleActivityDays lists the days that can be asked about: today, which is
// worked out on demand, and every day with a stored summary.
func (d Deps) handleActivityDays(w http.ResponseWriter, r *http.Request) {
	days := []string{time.Now().UTC().Format("20060102")}
	if d.DB != nil {
		stored, err := d.DB.ActivityDays(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		for _, day := range stored {
			if day != days[0] {
				days = append(days, day)
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"days": days})
}

func (d Deps) handleActivity(w http.ResponseWriter, r *http.Request) {
	if d.DB == nil {
		writeJSON(w, http.StatusOK, map[string]any{"activity": []any{}})
		return
	}
	day := r.URL.Query().Get("day")
	if day == "" {
		day = time.Now().UTC().Format("20060102")
	}

	// The rollover runs hourly, which would leave today looking empty for up
	// to an hour, so asking refreshes today and yesterday — at most every few
	// minutes, and yesterday only until it has been read after midnight.
	now := time.Now().UTC()
	if day == now.Format("20060102") || day == now.AddDate(0, 0, -1).Format("20060102") {
		if _, err := d.DB.RefreshActivity(r.Context(), day, now); err != nil {
			d.Log.Debug("could not refresh activity", "day", day, "error", err)
		}
	}

	rows, err := d.DB.ActivityFor(r.Context(), day)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"day": day, "activity": rows})
}

// maxBackups is how many backups may sit on the server. Each is a full copy
// of the database on the same disk, so past it a new one is refused rather
// than an old one deleted behind anyone's back.
const maxBackups = 3

const partialSuffix = ".partial"

var (
	backupMu       sync.Mutex
	msgBackupLimit = fmt.Sprintf("Up to %d backups are kept. Download what you need and delete one to make a new backup.", maxBackups)
	msgBackupBusy  = "A backup is already being written."
	msgNoBackups   = "backups are not available"
)

// handleBackup copies the database with VACUUM INTO, which is safe while the
// server is running. Restoring is simply starting against the copy: the hot
// state rebuilds itself from it. The copy is written under a
// temporary name and renamed when complete, so a half-written file never
// looks like a backup, and it carries on if the browser goes away.
func (d Deps) handleBackup(w http.ResponseWriter, r *http.Request) {
	if d.DB == nil || d.DataDir == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": msgNoBackups})
		return
	}
	if !backupMu.TryLock() {
		writeJSON(w, http.StatusConflict, map[string]string{"error": msgBackupBusy, "reason": "busy"})
		return
	}
	defer backupMu.Unlock()

	if err := os.MkdirAll(d.DataDir, 0o750); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	existing, err := listBackups(d.DataDir)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if len(existing) >= maxBackups {
		writeJSON(w, http.StatusConflict, map[string]string{"error": msgBackupLimit, "reason": "limit"})
		return
	}
	removePartials(d.DataDir)

	name, path := freeBackupName(d.DataDir, time.Now().UTC())
	partial := path + partialSuffix
	if err := d.DB.Backup(context.WithoutCancel(r.Context()), partial); err != nil {
		_ = os.Remove(partial)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := os.Rename(partial, path); err != nil {
		_ = os.Remove(partial)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	d.Log.Info("backup written", "path", path)
	var size int64
	if info, err := os.Stat(path); err == nil {
		size = info.Size()
	}
	writeJSON(w, http.StatusOK, map[string]any{"file": name, "path": path, "bytes": size})
}

// freeBackupName names a backup after the second it was made, adding a number
// when a backup from the same second is still there.
func freeBackupName(dir string, at time.Time) (string, string) {
	stamp := at.Format("20060102-150405")
	name := "hm-backup-" + stamp + ".db"
	for n := 2; ; n++ {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return name, path
		}
		name = fmt.Sprintf("hm-backup-%s-%d.db", stamp, n)
	}
}

// removePartials clears what an interrupted backup left behind. Callers hold
// backupMu, so no backup is being written.
func removePartials(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if name := e.Name(); strings.HasSuffix(name, partialSuffix) && backupName.MatchString(strings.TrimSuffix(name, partialSuffix)) {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
}

// BackupFile is one backup kept on the server.
type BackupFile struct {
	File    string    `json:"file"`
	Bytes   int64     `json:"bytes"`
	Created time.Time `json:"created"`
}

func listBackups(dir string) ([]BackupFile, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return []BackupFile{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]BackupFile, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !backupName.MatchString(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, BackupFile{File: e.Name(), Bytes: info.Size(), Created: info.ModTime()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].File > out[j].File })
	return out, nil
}

// handleBackups lists what has been backed up and is still on disk, newest
// first, so a copy can be fetched later rather than only at the moment it is
// made. Limit says how many may be kept.
func (d Deps) handleBackups(w http.ResponseWriter, r *http.Request) {
	if d.DataDir == "" {
		writeJSON(w, http.StatusOK, map[string]any{"backups": []any{}, "limit": maxBackups})
		return
	}
	out, err := listBackups(d.DataDir)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"backups": out, "limit": maxBackups})
}

// handleDeleteBackup removes one backup for good. Backups are never pruned on
// a timer, so this is the only way they go.
func (d Deps) handleDeleteBackup(w http.ResponseWriter, r *http.Request) {
	if d.DataDir == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "backups are not available"})
		return
	}
	name := r.PathValue("file")
	if !backupName.MatchString(name) {
		// The live database is not a backup, and neither is anything that has
		// to be cleaned up before it can be used as a path.
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "not a backup"})
		return
	}
	path := filepath.Join(d.DataDir, name)
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such backup"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	d.Log.Info("backup deleted", "path", path)
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted", "file": name})
}

// handleBackupFile sends one backup to the browser. It is the whole database,
// session_secret included, so it is behind the same login as everything else
// and out of a display token's reach.
func (d Deps) handleBackupFile(w http.ResponseWriter, r *http.Request) {
	if d.DataDir == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "backups are not available"})
		return
	}
	name := r.PathValue("file")
	if !backupName.MatchString(name) {
		// Anything that is not exactly a backup's name — a path, an escape, a
		// guess at hm.db — never reaches the filesystem.
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "not a backup"})
		return
	}
	path := filepath.Join(d.DataDir, name)
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such backup"})
		return
	}
	w.Header().Set("Content-Type", "application/vnd.sqlite3")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, name))
	http.ServeFile(w, r, path)
}
