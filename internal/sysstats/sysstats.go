// Package sysstats samples what the monitor process itself costs: memory,
// CPU, goroutines, open files, and the limits of the container it runs in.
package sysstats

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"runtime/metrics"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Sample is one reading.
type Sample struct {
	At         time.Time `json:"at"`
	RSSBytes   int64     `json:"rssBytes"`
	CPUPercent float64   `json:"cpuPercent"`
	// PacketsPerSec is the intake rate since the previous sample.
	PacketsPerSec float64 `json:"packetsPerSec"`
}

// Snapshot is the current state plus the last hour of samples.
type Snapshot struct {
	RSSBytes       int64   `json:"rssBytes"`
	HeapBytes      int64   `json:"heapBytes"`
	MemLimitBytes  int64   `json:"memLimitBytes,omitempty"`
	CPUPercent     float64 `json:"cpuPercent"`
	CPUPercentMin1 float64 `json:"cpuPercent1m"`
	PacketsPerSec  float64 `json:"packetsPerSec"`
	// PacketsLastMinute counts the last complete clock minute; absent until
	// one has passed.
	PacketsLastMinute *int64 `json:"packetsLastMinute,omitempty"`
	// LogsLastMinute counts the log rows written in the last complete minute.
	LogsLastMinute *int64    `json:"logsLastMinute,omitempty"`
	Cores          float64   `json:"cores"`
	Goroutines     int       `json:"goroutines"`
	OpenFiles      int       `json:"openFiles,omitempty"`
	GCCycles       uint64    `json:"gcCycles"`
	Started        time.Time `json:"started"`
	UptimeSeconds  int64     `json:"uptimeSeconds"`
	DBBytes        int64     `json:"dbBytes,omitempty"`
	History        []Sample  `json:"history,omitempty"`
}

// Sampler reads the process every interval and keeps a window of samples.
// CPUPercent is of one core, so 200 means two cores fully busy.
type Sampler struct {
	every   time.Duration
	keep    int
	started time.Time
	now     func() time.Time
	cpu     func() time.Duration
	packets func() int64
	logs    func() int64

	mu          sync.Mutex
	history     []Sample
	lastAt      time.Time
	lastCPU     time.Duration
	lastPackets int64

	packetMinute minuteCount
	logMinute    minuteCount
}

// minuteCount turns a running total into how much it grew in the last
// complete clock minute.
type minuteCount struct {
	minute time.Time
	from   int64
	known  bool
	last   *int64
}

func (m *minuteCount) observe(at time.Time, total int64) {
	minute := at.Truncate(time.Minute)
	if minute.Equal(m.minute) {
		return
	}
	if m.known {
		n := total - m.from
		m.last = &n
	}
	m.known = !m.minute.IsZero()
	m.minute, m.from = minute, total
}

// New keeps an hour of samples taken every interval.
func New(every time.Duration) *Sampler {
	if every <= 0 {
		every = 10 * time.Second
	}
	now := time.Now
	return &Sampler{
		every: every, keep: int(time.Hour / every), started: now(), now: now, cpu: processCPU,
	}
}

// CountPackets gives the sampler the intake's running packet total; call it
// before Run.
func (s *Sampler) CountPackets(total func() int64) { s.packets = total }

// CountLogs gives the sampler the running total of log rows written; call it
// before Run.
func (s *Sampler) CountLogs(total func() int64) { s.logs = total }

// Run samples until ctx is cancelled.
func (s *Sampler) Run(ctx context.Context) {
	s.sample()
	t := time.NewTicker(s.every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.sample()
		}
	}
}

func (s *Sampler) sample() {
	at, cpu := s.now(), s.cpu()
	var packets, logs int64
	if s.packets != nil {
		packets = s.packets()
	}
	if s.logs != nil {
		logs = s.logs()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	percent, rate := 0.0, 0.0
	if !s.lastAt.IsZero() {
		if wall := at.Sub(s.lastAt); wall > 0 {
			percent = 100 * float64(cpu-s.lastCPU) / float64(wall)
			rate = float64(packets-s.lastPackets) / wall.Seconds()
		}
	}
	s.lastAt, s.lastCPU, s.lastPackets = at, cpu, packets
	s.packetMinute.observe(at, packets)
	if s.logs != nil {
		s.logMinute.observe(at, logs)
	}
	s.history = append(s.history, Sample{
		At: at, RSSBytes: residentBytes(), CPUPercent: round1(percent), PacketsPerSec: round1(rate),
	})
	if len(s.history) > s.keep {
		s.history = s.history[len(s.history)-s.keep:]
	}
}

// Snapshot reports the latest reading; withHistory adds the last hour.
func (s *Sampler) Snapshot(withHistory bool) Snapshot {
	s.mu.Lock()
	history := append([]Sample(nil), s.history...)
	lastMinute, logsMinute := s.packetMinute.last, s.logMinute.last
	s.mu.Unlock()

	out := Snapshot{
		HeapBytes:     heapBytes(),
		MemLimitBytes: MemoryLimit(),
		Cores:         cores(),
		Goroutines:    runtime.NumGoroutine(),
		OpenFiles:     openFiles(),
		GCCycles:      gcCycles(),
		Started:       s.started,
		UptimeSeconds: int64(s.now().Sub(s.started).Seconds()),

		PacketsLastMinute: lastMinute,
		LogsLastMinute:    logsMinute,
	}
	if n := len(history); n > 0 {
		out.RSSBytes = history[n-1].RSSBytes
		out.CPUPercent = history[n-1].CPUPercent
		out.PacketsPerSec = history[n-1].PacketsPerSec
		window := min(n, int(time.Minute/s.every))
		sum := 0.0
		for _, h := range history[n-window:] {
			sum += h.CPUPercent
		}
		out.CPUPercentMin1 = round1(sum / float64(window))
	}
	if withHistory {
		out.History = history
	}
	return out
}

func processCPU() time.Duration {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0
	}
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
}

// residentBytes is what the kernel counts against the process (and its
// container), falling back to what the Go runtime holds.
func residentBytes() int64 {
	if raw, err := os.ReadFile("/proc/self/statm"); err == nil {
		if f := strings.Fields(string(raw)); len(f) > 1 {
			if pages, err := strconv.ParseInt(f[1], 10, 64); err == nil {
				return pages * int64(os.Getpagesize())
			}
		}
	}
	return int64(readUint("/memory/classes/total:bytes"))
}

func heapBytes() int64 { return int64(readUint("/memory/classes/heap/objects:bytes")) }

func gcCycles() uint64 { return readUint("/gc/cycles/total:gc-cycles") }

func readUint(name string) uint64 {
	s := []metrics.Sample{{Name: name}}
	metrics.Read(s)
	if s[0].Value.Kind() != metrics.KindUint64 {
		return 0
	}
	return s[0].Value.Uint64()
}

func openFiles() int {
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return 0
	}
	return len(entries)
}

// cgroupFile finds a cgroup v2 control file: at the root inside a container,
// under the process's own cgroup path on a host.
func cgroupFile(name string) string {
	if raw, err := os.ReadFile("/proc/self/cgroup"); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			if path, ok := strings.CutPrefix(line, "0::"); ok {
				candidate := filepath.Join("/sys/fs/cgroup", path, name)
				if _, err := os.Stat(candidate); err == nil {
					return candidate
				}
			}
		}
	}
	return filepath.Join("/sys/fs/cgroup", name)
}

// MemoryLimit is the container's memory limit; 0 when there is none.
func MemoryLimit() int64 {
	raw, err := os.ReadFile(cgroupFile("memory.max"))
	if err != nil {
		return 0
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// cores is how many CPUs the process may use: the container's quota when it
// has one, else what the scheduler offers.
func cores() float64 {
	if raw, err := os.ReadFile(cgroupFile("cpu.max")); err == nil {
		if f := strings.Fields(string(raw)); len(f) == 2 && f[0] != "max" {
			quota, err1 := strconv.ParseFloat(f[0], 64)
			period, err2 := strconv.ParseFloat(f[1], 64)
			if err1 == nil && err2 == nil && period > 0 {
				return round1(quota / period)
			}
		}
	}
	return float64(runtime.NumCPU())
}

func round1(v float64) float64 {
	return float64(int64(v*10+0.5)) / 10
}
