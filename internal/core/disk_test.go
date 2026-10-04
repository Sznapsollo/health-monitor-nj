package core_test

import (
	"strings"
	"testing"

	"github.com/Sznapsollo/health-monitor-nj/internal/alert"
	"github.com/Sznapsollo/health-monitor-nj/internal/core"
	"github.com/Sznapsollo/health-monitor-nj/internal/store"
)

func TestADiskRunningLowIsAlertedOnceAndSoIsItsRecovery(t *testing.T) {
	f := newFixture(t)
	const gb = int64(1) << 30
	free := 50 * gb
	f.core.SetDiskWatch(core.DiskWatch{
		Folders: map[string]string{"database": "/data", "archive": "/data/archive"},
		MinFree: 10 * gb,
		Measure: func(string) (store.DiskSpace, bool) {
			return store.DiskSpace{Device: "disk1", Free: free, Total: 600 * gb}, true
		},
	})

	f.core.CheckDisk()
	readings := f.core.DiskReadings()
	if len(readings) != 1 || strings.Join(readings[0].Folders, ",") != "archive,database" || readings[0].Low {
		t.Fatalf("readings = %+v, want one disk holding both folders, not low", readings)
	}
	if got := f.alerts.List("test", alert.Filter{}); len(got) != 0 {
		t.Fatalf("alerts = %+v with plenty of space", got)
	}

	free = 8 * gb
	f.core.CheckDisk()
	f.core.CheckDisk()
	got := f.alerts.List("test", alert.Filter{})
	if len(got) != 1 || got[0].Level != "ERROR" || got[0].Category != "disk" || got[0].Count != 1 ||
		!strings.Contains(got[0].Message, "8.0 GB free") || !strings.Contains(got[0].Message, "archive and database") {
		t.Fatalf("alerts = %+v, want one error while it stays low", got)
	}
	if !f.core.DiskReadings()[0].Low {
		t.Error("reading not marked low")
	}

	free = 30 * gb
	f.core.CheckDisk()
	got = f.alerts.List("test", alert.Filter{})
	if len(got) != 2 || got[0].Level != "INFO" || !strings.Contains(got[0].Message, "back") {
		t.Errorf("alerts = %+v, want a recovery after the error", got)
	}
}
