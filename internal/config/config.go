// Package config loads config.yaml and applies HM_* environment overrides.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the whole server configuration. Every field has a working default,
// so an empty file (or no file at all) starts a usable dev server.
type Config struct {
	Server Server `yaml:"server"`
	Log    Log    `yaml:"log"`
	Auth   Auth   `yaml:"auth"`
	Intake Intake `yaml:"intake"`
	State  State  `yaml:"state"`
	Hub    Hub    `yaml:"hub"`
	Data   Data   `yaml:"data"`
}

// Server holds the listener addresses.
type Server struct {
	HTTPAddr  string `yaml:"http_addr"`
	UDPAddr   string `yaml:"udp_addr"`
	AdminAddr string `yaml:"admin_addr"`
	// AllowedOrigins are the browser origins that may open a WebSocket.
	// Empty means same-origin only; the Vite dev server needs its own entry.
	AllowedOrigins []string `yaml:"allowed_origins"`
}

// State bounds the hot, in-memory half of the server.
type State struct {
	// MaxHotMB is the memory guard: past it the oldest dimension
	// breakdowns are dropped, oldest first. Totals are never evicted, and
	// SQLite still has everything. Zero picks a limit (see HotLimit); -1
	// switches the guard off.
	MaxHotMB int `yaml:"max_hot_mb"`
}

// DefaultHotMB is the guard when neither the config nor a container limit
// says otherwise.
const DefaultHotMB = 512

// Measured under load (docs/perf.md, "Memory under a container limit"): the
// process holds about hotFixedBytes plus hotMemoryFactor times the hot-state
// estimate, so the guard is sized to keep that under nine tenths of the limit.
const (
	hotFixedBytes   = 120 << 20
	hotMemoryFactor = 2.2
	minHotBytes     = 16 << 20
	// SmallContainerBytes is where that leaves too little for hot state to be
	// worth having; the server warns below it.
	SmallContainerBytes = 384 << 20
)

// HotLimit resolves the memory guard in bytes, and says where it came from:
// the config, what fits the container's memory limit, or DefaultHotMB.
func (s State) HotLimit(containerLimit int64) (int64, string) {
	switch {
	case s.MaxHotMB > 0:
		return int64(s.MaxHotMB) << 20, "config"
	case s.MaxHotMB < 0:
		return 0, "off"
	case containerLimit > 0:
		fits := int64(float64(containerLimit/10*9-hotFixedBytes) / hotMemoryFactor)
		return max(fits>>20<<20, minHotBytes), "fitted to the container limit"
	default:
		return DefaultHotMB << 20, "default"
	}
}

// Hub paces what connected viewers receive.
type Hub struct {
	// BroadcastDelay is how long changes are collected before an update is
	// sent, the old event bus's 4 s.
	BroadcastDelay time.Duration `yaml:"broadcast_delay"`
}

// Log configures the structured logger.
type Log struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

// Auth holds the dashboard credentials. The password never lives in the repo;
// HM_PASS is the normal way to supply it.
type Auth struct {
	Password string `yaml:"password"`
	Open     bool   `yaml:"open"`
}

// Intake configures the UDP listeners.
type Intake struct {
	Readers    int `yaml:"readers"`
	ReadBuffer int `yaml:"read_buffer_bytes"`
	MaxPacket  int `yaml:"max_packet_bytes"`
	// DefaultPlatform labels packets that name no platform of their own and
	// arrive on no platform-specific port.
	DefaultPlatform string `yaml:"default_platform"`
	// DailyLogs records one searchable row per request, as the old server
	// did. A high-volume platform turns it off and keeps only the charts.
	DailyLogs bool `yaml:"daily_logs"`
	// DropOlderThan rejects packets whose own timestamp is far in the past,
	// the old server's "start date anomaly" guard. Zero accepts anything.
	DropOlderThan time.Duration `yaml:"drop_older_than"`
	// DropNewerThan rejects packets stamped further ahead than this, so a
	// sender with a wrong clock cannot fill memory with minutes that are
	// never evicted. Zero accepts anything.
	DropNewerThan time.Duration `yaml:"drop_newer_than"`
}

// Data holds the on-disk locations and how often they are written.
type Data struct {
	Dir          string `yaml:"dir"`
	PlatformsDir string `yaml:"platforms_dir"`
	// SeedPlatformsFrom fills platforms_dir with any file it lacks from this
	// directory at start; the Docker image points it at its shipped copy.
	SeedPlatformsFrom string `yaml:"seed_platforms_from"`
	// FlushEvery bounds how long a log row or an alert waits before it is
	// durable.
	FlushEvery time.Duration `yaml:"flush_every"`
	// AggregateFlushEvery bounds how long the minute in progress waits; a
	// minute is written as soon as it closes, so this is what a crash loses.
	AggregateFlushEvery time.Duration `yaml:"aggregate_flush_every"`
	// LogKeepDays is how many days of searchable rows are kept, for any kind
	// that LogKeepDaysByKind does not name.
	LogKeepDays int `yaml:"log_keep_days"`
	// LogKeepDaysByKind overrides that per kind of log, because the bulky ones
	// are worth keeping for less time than the rest: dailyLogs for 2 days
	// and everything else for 14.
	LogKeepDaysByKind map[string]int `yaml:"log_keep_days_by_kind"`
	// LogArchiveKeepDays is how long a day of logs is kept as a gzip file under
	// dir/archive after it leaves the database, counted from when the file
	// was written; 0 drops old days instead.
	LogArchiveKeepDays int `yaml:"log_archive_keep_days"`
	// LogArchiveKeepDaysByKind overrides that per kind; 0 turns it off.
	LogArchiveKeepDaysByKind map[string]int `yaml:"log_archive_keep_days_by_kind"`
	// AlertKeepDays is how long the alert history is kept. The live alerts in
	// memory are a separate, much shorter window.
	AlertKeepDays int `yaml:"alert_keep_days"`
	// AlertArchiveKeepDays is how long a day of alerts is kept as a gzip file
	// under dir/archive after it leaves the database, counted from when the
	// file was written; 0 deletes old alerts instead.
	AlertArchiveKeepDays int `yaml:"alert_archive_keep_days"`
	// MinFreeGB raises an alert when the disk holding the database or the
	// archive has less free space than this; 0 only measures.
	MinFreeGB float64 `yaml:"min_free_gb"`
	// ViewerHistoryKeepDays is how long the history of who had the monitor
	// open is kept, counted from when each visit ended.
	ViewerHistoryKeepDays int `yaml:"viewer_history_keep_days"`
	// ActivityKeepDays is how long the per-user activity summary is kept.
	ActivityKeepDays int `yaml:"activity_keep_days"`
	// BackupDir is where database backups are written; empty is Dir, next
	// to the database.
	BackupDir string `yaml:"backup_dir"`
}

// Backups is the folder database backups go to.
func (d Data) Backups() string {
	if d.BackupDir != "" {
		return d.BackupDir
	}
	return d.Dir
}

// Default returns the configuration used when nothing is supplied.
func Default() Config {
	return Config{
		Server: Server{
			HTTPAddr:  ":8081",
			UDPAddr:   ":8082",
			AdminAddr: "127.0.0.1:8083",
		},
		Log: Log{Level: "info", Format: "json"},
		Intake: Intake{
			Readers:         0,
			ReadBuffer:      8 << 20,
			MaxPacket:       16 << 10,
			DefaultPlatform: "example",
			DailyLogs:       true,
			DropOlderThan:   0,
			DropNewerThan:   2 * time.Minute,
		},
		Data: Data{
			Dir:                   "./data",
			PlatformsDir:          "./platforms",
			FlushEvery:            time.Second,
			AggregateFlushEvery:   15 * time.Second,
			LogKeepDays:           14,
			LogKeepDaysByKind:     map[string]int{"dailyLogs": 2},
			LogArchiveKeepDays:    10,
			AlertKeepDays:         10,
			AlertArchiveKeepDays:  10,
			ActivityKeepDays:      31,
			MinFreeGB:             10,
			ViewerHistoryKeepDays: 30,
		},
	}
}

// Load reads path (missing file is not an error), then applies environment
// overrides and validates the result.
func Load(path string) (Config, error) {
	c := Default()
	if path != "" {
		b, err := os.ReadFile(path)
		switch {
		case err == nil:
			if err := yaml.Unmarshal(b, &c); err != nil {
				return c, fmt.Errorf("config: parse %s: %w", path, err)
			}
		case os.IsNotExist(err):
		default:
			return c, fmt.Errorf("config: read %s: %w", path, err)
		}
	}
	if err := c.applyEnv(os.LookupEnv); err != nil {
		return c, err
	}
	return c, c.Validate()
}

type lookupFunc func(string) (string, bool)

func (c *Config) applyEnv(look lookupFunc) error {
	// An empty variable counts as unset: a compose file that declares
	// HM_PASS= must not blank out what config.yaml provides.
	str := func(key string, dst *string) {
		if v, ok := look(key); ok && v != "" {
			*dst = v
		}
	}
	num := func(key string, dst *int) error {
		v, ok := look(key)
		if !ok || v == "" {
			return nil
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("config: %s: %w", key, err)
		}
		*dst = n
		return nil
	}
	str("HM_HTTP_ADDR", &c.Server.HTTPAddr)
	str("HM_UDP_ADDR", &c.Server.UDPAddr)
	str("HM_ADMIN_ADDR", &c.Server.AdminAddr)
	str("HM_LOG_LEVEL", &c.Log.Level)
	str("HM_LOG_FORMAT", &c.Log.Format)
	str("HM_PASS", &c.Auth.Password)
	if v, ok := look("HM_OPEN"); ok && v != "" {
		open, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("config: HM_OPEN: %w", err)
		}
		c.Auth.Open = open
	}
	str("HM_DEFAULT_PLATFORM", &c.Intake.DefaultPlatform)
	str("HM_DATA_DIR", &c.Data.Dir)
	str("HM_BACKUP_DIR", &c.Data.BackupDir)
	str("HM_PLATFORMS_DIR", &c.Data.PlatformsDir)
	str("HM_SEED_PLATFORMS_FROM", &c.Data.SeedPlatformsFrom)
	if err := num("HM_INTAKE_READERS", &c.Intake.Readers); err != nil {
		return err
	}
	if err := num("HM_INTAKE_READ_BUFFER_BYTES", &c.Intake.ReadBuffer); err != nil {
		return err
	}
	return num("HM_MAX_PACKET_BYTES", &c.Intake.MaxPacket)
}

// Validate reports configuration that cannot work.
func (c Config) Validate() error {
	if c.Server.HTTPAddr == "" {
		return fmt.Errorf("config: server.http_addr is empty")
	}
	if c.Server.UDPAddr == "" {
		return fmt.Errorf("config: server.udp_addr is empty")
	}
	switch strings.ToLower(c.Log.Format) {
	case "json", "text":
	default:
		return fmt.Errorf("config: log.format %q is not json or text", c.Log.Format)
	}
	switch strings.ToLower(c.Log.Level) {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("config: log.level %q is not debug, info, warn or error", c.Log.Level)
	}
	if c.Intake.Readers < 0 {
		return fmt.Errorf("config: intake.readers is negative")
	}
	if c.Intake.MaxPacket < 512 {
		return fmt.Errorf("config: intake.max_packet_bytes is below 512")
	}
	if c.Intake.DefaultPlatform == "" {
		return fmt.Errorf("config: intake.default_platform is empty")
	}
	if c.Hub.BroadcastDelay < 0 {
		return fmt.Errorf("config: hub.broadcast_delay is negative")
	}
	if c.State.MaxHotMB < -1 {
		return fmt.Errorf("config: state.max_hot_mb must be -1 (off), 0 (automatic) or a size in MB")
	}
	return nil
}
