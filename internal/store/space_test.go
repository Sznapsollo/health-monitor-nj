package store_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sznapsollo/health-monitor-nj/internal/store"

	_ "modernc.org/sqlite"
)

func autoVacuum(t *testing.T, s *store.Store) int {
	t.Helper()
	var mode int
	if err := s.DB().QueryRow(`PRAGMA auto_vacuum`).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	return mode
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Size()
}

func TestANewDatabaseReturnsFreedSpace(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := store.Open(store.Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if got := autoVacuum(t, s); got != 2 {
		t.Fatalf("auto_vacuum = %d, want incremental from the start", got)
	}

	if _, err := s.DB().Exec(`CREATE TABLE filler (x TEXT)`); err != nil {
		t.Fatal(err)
	}
	pad := strings.Repeat("x", 4000)
	for i := 0; i < 2000; i++ {
		if _, err := s.DB().Exec(`INSERT INTO filler VALUES (?)`, pad); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.DB().Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	full := fileSize(t, s.Path())
	if _, err := s.DB().Exec(`DROP TABLE filler`); err != nil {
		t.Fatal(err)
	}

	freed, err := s.ReclaimSpace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if freed < 4<<20 {
		t.Errorf("freed %d bytes, want the dropped table's ~8 MB", freed)
	}
	if after := fileSize(t, s.Path()); after >= full/2 {
		t.Errorf("file %d bytes after reclaiming, was %d; want it to shrink", after, full)
	}
}

func TestAnOldDatabaseIsSwitchedOnce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hm.db")
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(`CREATE TABLE minute_agg (x TEXT)`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 500; i++ {
		if _, err := old.Exec(`INSERT INTO minute_agg VALUES (?)`, fmt.Sprint(strings.Repeat("y", 4000), i)); err != nil {
			t.Fatal(err)
		}
	}
	_ = old.Close()
	before := fileSize(t, path)

	s, err := store.Open(store.Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if got := autoVacuum(t, s); got != 2 {
		t.Fatalf("auto_vacuum = %d, want the old database switched to incremental", got)
	}
	if after := fileSize(t, path); after >= before/2 {
		t.Errorf("file %d bytes, was %d; want the dropped minute_agg compacted away", after, before)
	}
}
