package store_test

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/store"
)

func logRow(ts time.Time, signal string, payload map[string]any) store.LogRow {
	return store.LogRow{
		Platform: "test", Signal: signal, TS: ts,
		Account: "acme", User: "anna", Payload: payload,
	}
}

func TestWriteAndSearchLogs(t *testing.T) {
	s := open(t)
	ctx := context.Background()

	rows := []store.LogRow{
		logRow(base, "sendLogs", map[string]any{
			"to": "user@example.test", "subject": "Order confirmation", "emailType": "orderConfirmation",
		}),
		logRow(base.Add(time.Minute), "sendLogs", map[string]any{
			"to": "other@example.test", "subject": "Password reset",
		}),
		logRow(base.Add(2*time.Minute), "dailyLogs", map[string]any{"url": "/api/work"}),
	}
	if err := s.WriteLogs(ctx, rows); err != nil {
		t.Fatalf("write: %v", err)
	}

	all, err := s.SearchLogs(ctx, store.LogQuery{Platform: "test"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("rows = %d, want all three", len(all))
	}
	// Newest first, as the panel shows them.
	if all[0].Signal != "dailyLogs" {
		t.Errorf("first = %+v, want the newest", all[0])
	}

	byKind, err := s.SearchLogs(ctx, store.LogQuery{Platform: "test", Signal: "sendLogs"})
	if err != nil {
		t.Fatal(err)
	}
	if len(byKind) != 2 {
		t.Errorf("sendLogs rows = %d", len(byKind))
	}
}

func TestFullTextSearch(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	if err := s.WriteLogs(ctx, []store.LogRow{
		logRow(base, "sendLogs", map[string]any{"to": "user@example.test", "subject": "Order confirmation"}),
		logRow(base.Add(time.Minute), "sendLogs", map[string]any{"to": "other@example.test", "subject": "Password reset"}),
	}); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name, text string
		want       int
	}{
		{name: "a word in the payload", text: "confirmation", want: 1},
		{name: "an address", text: "other@example.test", want: 1},
		{name: "any of several terms", text: "nothing, reset", want: 1},
		{name: "matches both", text: "example.test", want: 2},
		{name: "no match", text: "unicorn", want: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := s.SearchLogs(ctx, store.LogQuery{Platform: "test", Text: tc.text})
			if err != nil {
				t.Fatalf("search: %v", err)
			}
			if len(got) != tc.want {
				t.Fatalf("rows = %d, want %d: %+v", len(got), tc.want, got)
			}
		})
	}
}

func TestSearchSyntaxCannotEscapeIntoFTS(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	if err := s.WriteLogs(ctx, []store.LogRow{
		logRow(base, "sendLogs", map[string]any{"subject": "quarterly report"}),
	}); err != nil {
		t.Fatal(err)
	}
	// Terms that would otherwise be read as FTS operators must be treated as
	// text, not blow up the query.
	for _, text := range []string{`NOT report`, `"`, `report OR`, `*`, `(`} {
		if _, err := s.SearchLogs(ctx, store.LogQuery{Platform: "test", Text: text}); err != nil {
			t.Errorf("search %q: %v", text, err)
		}
	}
}

func TestSearchAcrossDays(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	yesterday := base.Add(-24 * time.Hour)
	if err := s.WriteLogs(ctx, []store.LogRow{
		logRow(yesterday, "sendLogs", map[string]any{"subject": "yesterday's mail"}),
		logRow(base, "sendLogs", map[string]any{"subject": "today's mail"}),
	}); err != nil {
		t.Fatal(err)
	}

	days, err := s.LogDays(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 2 {
		t.Fatalf("days = %v, want one table per day", days)
	}

	// The old panel could only see today; this one answers across days.
	all, err := s.SearchLogs(ctx, store.LogQuery{Platform: "test", Text: "mail"})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("rows = %d, want both days", len(all))
	}
	if all[0].TS.Before(all[1].TS) {
		t.Error("rows are not newest first across days")
	}

	// A window keeps the search to the days it covers.
	only, err := s.SearchLogs(ctx, store.LogQuery{
		Platform: "test", From: base.Add(-time.Hour), To: base.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(only) != 1 {
		t.Fatalf("rows = %d, want only today's", len(only))
	}
}

func TestLimitReturnsTheNewest(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	var rows []store.LogRow
	for i := 0; i < 20; i++ {
		rows = append(rows, logRow(base.Add(time.Duration(i)*time.Minute), "sendLogs",
			map[string]any{"n": i}))
	}
	if err := s.WriteLogs(ctx, rows); err != nil {
		t.Fatal(err)
	}

	got, err := s.SearchLogs(ctx, store.LogQuery{Platform: "test", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 {
		t.Fatalf("rows = %d, want the limit", len(got))
	}
	if n, _ := got[0].Payload["n"].(float64); n != 19 {
		t.Errorf("first row = %v, want the newest", got[0].Payload["n"])
	}
}

func TestRetentionDropsAWholeDay(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	old := base.Add(-10 * 24 * time.Hour)
	if err := s.WriteLogs(ctx, []store.LogRow{
		logRow(old, "sendLogs", map[string]any{"subject": "ancient"}),
		logRow(base, "sendLogs", map[string]any{"subject": "recent"}),
	}); err != nil {
		t.Fatal(err)
	}

	n, err := s.CountLogs(ctx, old.UTC().Format("20060102"))
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("count = %d", n)
	}

	if err := s.DropLogDay(ctx, old.UTC().Format("20060102")); err != nil {
		t.Fatalf("drop: %v", err)
	}
	days, _ := s.LogDays(ctx)
	if len(days) != 1 {
		t.Fatalf("days = %v, want the old one gone", days)
	}
	left, err := s.SearchLogs(ctx, store.LogQuery{Platform: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 1 || left[0].Payload["subject"] != "recent" {
		t.Fatalf("rows = %+v", left)
	}
}

func TestLogsSurviveAReopen(t *testing.T) {
	dir := t.TempDir()
	first, err := store.Open(store.Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.WriteLogs(context.Background(), []store.LogRow{
		logRow(base, "sendLogs", map[string]any{"subject": "kept"}),
	}); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := store.Open(store.Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()

	got, err := second.SearchLogs(context.Background(), store.LogQuery{Platform: "test", Text: "kept"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("rows = %+v, want what was written before the restart", got)
	}
}

func TestEachKindGetsItsOwnTable(t *testing.T) {
	s := open(t)
	ctx := context.Background()

	err := s.WriteLogs(ctx, []store.LogRow{
		logRow(base, "sendLogs", map[string]any{"to": "user@example.test"}),
		logRow(base.Add(time.Minute), "dailyLogs", map[string]any{"url": "/api/work"}),
		// A kind that cannot be a table name as it stands is spelled safely.
		logRow(base.Add(2*time.Minute), "odd.kind/2", map[string]any{"x": 1}),
	})
	if err != nil {
		t.Fatal(err)
	}

	tables, err := s.LogTables(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tables) != 3 {
		t.Fatalf("tables = %+v, want one per kind", tables)
	}
	byKind := map[string]string{}
	for _, tb := range tables {
		byKind[tb.Signal] = tb.Name()
	}
	if byKind["sendLogs"] != "log_"+base.UTC().Format("20060102")+"_sendLogs" {
		t.Errorf("table names = %+v, want the kind readable in the name", byKind)
	}
	// The awkward kind is still recoverable, whatever its table is called.
	if _, ok := byKind["odd.kind/2"]; !ok {
		t.Errorf("kinds = %+v, want the original name kept", byKind)
	}

	// Searching one kind reads that kind's table and nothing else.
	rows, err := s.SearchLogs(ctx, store.LogQuery{Platform: "test", Signal: "sendLogs"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Signal != "sendLogs" {
		t.Fatalf("rows = %+v, want only the sends", rows)
	}

	// With no kind named, every kind still answers, newest first.
	all, err := s.SearchLogs(ctx, store.LogQuery{Platform: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("rows = %d, want every kind", len(all))
	}
	for i := 1; i < len(all); i++ {
		if all[i].TS.After(all[i-1].TS) {
			t.Errorf("rows are not newest first: %+v", all)
		}
	}
}

func TestOneKindCanBeDroppedWithoutTheOthers(t *testing.T) {
	s := open(t)
	ctx := context.Background()

	err := s.WriteLogs(ctx, []store.LogRow{
		logRow(base, "sendLogs", map[string]any{"to": "user@example.test"}),
		logRow(base, "dailyLogs", map[string]any{"url": "/api/work"}),
	})
	if err != nil {
		t.Fatal(err)
	}

	tables, err := s.LogTables(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, tb := range tables {
		if tb.Signal != "dailyLogs" {
			continue
		}
		if err := s.DropLogTable(ctx, tb); err != nil {
			t.Fatal(err)
		}
	}

	// The bulky kind is gone; the rest of the day is untouched.
	rows, err := s.SearchLogs(ctx, store.LogQuery{Platform: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Signal != "sendLogs" {
		t.Fatalf("rows = %+v, want only the kind that was kept", rows)
	}
	n, err := s.CountLogs(ctx, base.UTC().Format("20060102"))
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("count = %d, want the day counted across its remaining kinds", n)
	}
}

func TestTheOldSharedLogTablesAreDropped(t *testing.T) {
	s := open(t)
	ctx := context.Background()

	// The shape logs had before the split: one table a day, every kind in it,
	// with its own index and FTS5's shadow tables hanging off that.
	for _, stmt := range []string{
		`CREATE TABLE log_20260101 (id INTEGER PRIMARY KEY AUTOINCREMENT, platform TEXT,
			signal TEXT, ts INTEGER, key TEXT, account TEXT, user TEXT, url TEXT,
			level TEXT, payload TEXT)`,
		`CREATE VIRTUAL TABLE log_fts_20260101 USING fts5(payload, content='log_20260101', content_rowid='id')`,
		`INSERT INTO log_20260101 (platform, signal, ts, payload) VALUES ('test', 'sendLogs', 1, '{}')`,
	} {
		if _, err := s.DB().ExecContext(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	// Something in the current shape, which must survive.
	if err := s.WriteLogs(ctx, []store.LogRow{logRow(base, "sendLogs", map[string]any{"to": "a@b.c"})}); err != nil {
		t.Fatal(err)
	}

	n, err := s.DropLegacyLogTables(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("dropped = %d, want the table and its index", n)
	}

	tables, err := s.LogTables(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tables) != 1 || tables[0].Signal != "sendLogs" || tables[0].Day != base.UTC().Format("20060102") {
		t.Errorf("tables = %+v, want only the current shape", tables)
	}
	// And running it again on a database that has none is a no-op.
	if n, err := s.DropLegacyLogTables(ctx); err != nil || n != 0 {
		t.Errorf("second run dropped %d, %v", n, err)
	}
}

func TestLiftedFieldsAreStoredOnceAndReadBackWhole(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	payload := map[string]any{"url": "/api/work", "user": "anna", "account": "42", "executionTime": 142.0, "subject": "Quarterly report"}
	err := s.WriteLogs(ctx, []store.LogRow{{
		Platform: "test", Signal: "dailyLogs", TS: base, Account: "42", User: "anna", URL: "/api/work",
		Payload: payload,
	}})
	if err != nil {
		t.Fatal(err)
	}

	var raw string
	table := "log_" + base.UTC().Format("20060102") + "_dailyLogs"
	if err := s.DB().QueryRow(`SELECT payload FROM ` + table).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{`"url"`, `"user"`, `"account"`} {
		if strings.Contains(raw, k) {
			t.Errorf("stored payload %s still repeats %s", raw, k)
		}
	}

	rows, err := s.SearchLogs(ctx, store.LogQuery{Platform: "test", From: base.Add(-time.Hour), To: base.Add(time.Hour)})
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows = %v, %v", rows, err)
	}
	if !reflect.DeepEqual(rows[0].Payload, payload) {
		t.Errorf("payload = %v, want what was written: %v", rows[0].Payload, payload)
	}

	for text, want := range map[string]int{"Quarterly": 1, "anna": 1, "142": 1, "subject": 0, "executionTime": 0} {
		got, err := s.SearchLogs(ctx, store.LogQuery{Platform: "test", Text: text, From: base.Add(-time.Hour), To: base.Add(time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != want {
			t.Errorf("search %q found %d, want %d", text, len(got), want)
		}
	}
}

func TestADayIsReadInPagesWithoutLosingOrRepeatingRows(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	const n = 12_001
	rows := make([]store.LogRow, n)
	for i := range rows {
		rows[i] = store.LogRow{Platform: "test", Signal: "dailyLogs", TS: base.Add(time.Duration(i) * time.Millisecond),
			Payload: map[string]any{"i": float64(i)}}
	}
	if err := s.WriteLogs(ctx, rows); err != nil {
		t.Fatal(err)
	}

	next := 0
	err := s.ForEachLog(ctx, store.LogQuery{Platform: "test", From: base.Add(-time.Hour), To: base.Add(time.Hour)},
		func(r store.LogRow) error {
			if got := int(r.Payload["i"].(float64)); got != next {
				t.Fatalf("row %d came back as %d", next, got)
			}
			next++
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if next != n {
		t.Errorf("read %d rows, want %d", next, n)
	}
}
