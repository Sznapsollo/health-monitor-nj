package core

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/alert"
	"github.com/Sznapsollo/health-monitor-nj/internal/protocol"
	"github.com/Sznapsollo/health-monitor-nj/internal/store"
)

// DiskWatch says which folders' disks to watch and how much free space is
// too little. MinFree of 0 still measures, but never alerts.
type DiskWatch struct {
	// Folders maps a name ("database", "archive") to its path.
	Folders map[string]string
	MinFree int64
	// Measure reads a folder's disk; nil reads the real one.
	Measure func(string) (store.DiskSpace, bool)
}

// DiskReading is one filesystem, with the watched folders on it.
type DiskReading struct {
	Folders []string `json:"folders"`
	store.DiskSpace
	Low bool `json:"low"`
}

type diskState struct {
	mu       sync.Mutex
	watch    DiskWatch
	readings []DiskReading
	low      map[string]bool
}

// SetDiskWatch starts measuring the disks behind these folders.
func (c *Core) SetDiskWatch(w DiskWatch) {
	c.disk.mu.Lock()
	defer c.disk.mu.Unlock()
	if w.Measure == nil {
		w.Measure = store.DiskSpaceOf
	}
	c.disk.watch = w
	if c.disk.low == nil {
		c.disk.low = map[string]bool{}
	}
}

// DiskReadings are the latest measurements, lowest free space first.
func (c *Core) DiskReadings() []DiskReading {
	c.disk.mu.Lock()
	defer c.disk.mu.Unlock()
	return append([]DiskReading(nil), c.disk.readings...)
}

// CheckDisk measures every watched disk and raises an alert when one falls
// below the limit, and another when it is back above it.
func (c *Core) CheckDisk() {
	c.disk.mu.Lock()
	watch := c.disk.watch
	c.disk.mu.Unlock()
	measure := watch.Measure
	if measure == nil {
		return
	}

	byDevice := map[string]*DiskReading{}
	var order []string
	for name, path := range watch.Folders {
		space, ok := measure(path)
		if !ok {
			continue
		}
		r, seen := byDevice[space.Device]
		if !seen {
			r = &DiskReading{DiskSpace: space}
			byDevice[space.Device] = r
			order = append(order, space.Device)
		}
		r.Folders = append(r.Folders, name)
	}

	var readings []DiskReading
	var changed []DiskReading
	c.disk.mu.Lock()
	for _, device := range order {
		r := byDevice[device]
		sort.Strings(r.Folders)
		r.Low = watch.MinFree > 0 && r.Free < watch.MinFree
		if r.Low != c.disk.low[device] {
			c.disk.low[device] = r.Low
			changed = append(changed, *r)
		}
		readings = append(readings, *r)
	}
	sort.Slice(readings, func(i, j int) bool { return readings[i].Free < readings[j].Free })
	c.disk.readings = readings
	c.disk.mu.Unlock()

	for _, r := range changed {
		c.raiseDisk(r, watch.MinFree)
	}
}

func (c *Core) raiseDisk(r DiskReading, minFree int64) {
	folders := strings.Join(r.Folders, " and ")
	level, key := protocol.LevelInfo, "disk-ok/"+folders
	message := fmt.Sprintf("disk space is back: %s free on the disk holding the %s", gigabytes(r.Free), folders)
	if r.Low {
		level, key = protocol.LevelError, "disk-low/"+folders
		message = fmt.Sprintf("disk almost full: %s free on the disk holding the %s, below the %s limit",
			gigabytes(r.Free), folders, gigabytes(minFree))
	}
	for _, platform := range c.reg.Platforms() {
		c.Raise(alert.Alert{
			Platform: platform,
			Level:    level,
			Category: "disk",
			Message:  message,
			GroupKey: key,
			Data: map[string]any{
				"freeBytes": r.Free, "totalBytes": r.Total, "minFreeBytes": minFree, "folders": r.Folders,
			},
			Last: c.now(),
		})
	}
	if r.Low {
		c.log.Error("disk almost full", "folders", folders, "free", gigabytes(r.Free), "limit", gigabytes(minFree))
	} else {
		c.log.Info("disk space is back", "folders", folders, "free", gigabytes(r.Free))
	}
}

func gigabytes(b int64) string { return fmt.Sprintf("%.1f GB", float64(b)/(1<<30)) }

// CheckDiskEvery measures the disks until ctx is cancelled.
func (c *Core) CheckDiskEvery(ctx context.Context, every time.Duration) {
	if every <= 0 {
		every = time.Minute
	}
	t := time.NewTicker(every)
	defer t.Stop()
	c.CheckDisk()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.CheckDisk()
		}
	}
}
