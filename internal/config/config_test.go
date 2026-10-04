package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaultsWhenFileMissing(t *testing.T) {
	t.Setenv("HM_PASS", "")
	got, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	want := Default()
	if got.Server.HTTPAddr != want.Server.HTTPAddr ||
		got.Server.UDPAddr != want.Server.UDPAddr ||
		got.Server.AdminAddr != want.Server.AdminAddr ||
		got.Log != want.Log ||
		got.Hub != want.Hub {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestLoadFileThenEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := `
server:
  http_addr: ":9001"
  udp_addr: ":9002"
log:
  level: warn
  format: text
intake:
  readers: 4
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	// An empty variable must not win over the file.
	t.Setenv("HM_HTTP_ADDR", "")
	t.Setenv("HM_UDP_ADDR", ":7777")
	t.Setenv("HM_PASS", "s3cret")
	t.Setenv("HM_INTAKE_READERS", "8")

	got, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Server.HTTPAddr != ":9001" {
		t.Errorf("http_addr = %q, want the file value", got.Server.HTTPAddr)
	}
	if got.Server.UDPAddr != ":7777" {
		t.Errorf("udp_addr = %q, want the env override", got.Server.UDPAddr)
	}
	if got.Auth.Password != "s3cret" {
		t.Errorf("password = %q", got.Auth.Password)
	}
	if got.Intake.Readers != 8 {
		t.Errorf("readers = %d, want the env override", got.Intake.Readers)
	}
	if got.Log.Format != "text" || got.Log.Level != "warn" {
		t.Errorf("log = %+v", got.Log)
	}
	// Untouched keys keep their defaults.
	if got.Data.Dir != Default().Data.Dir {
		t.Errorf("data.dir = %q", got.Data.Dir)
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr bool
	}{
		{name: "default is valid"},
		{name: "empty http addr", mutate: func(c *Config) { c.Server.HTTPAddr = "" }, wantErr: true},
		{name: "empty udp addr", mutate: func(c *Config) { c.Server.UDPAddr = "" }, wantErr: true},
		{name: "bad log format", mutate: func(c *Config) { c.Log.Format = "xml" }, wantErr: true},
		{name: "bad log level", mutate: func(c *Config) { c.Log.Level = "chatty" }, wantErr: true},
		{name: "negative readers", mutate: func(c *Config) { c.Intake.Readers = -1 }, wantErr: true},
		{name: "tiny packet cap", mutate: func(c *Config) { c.Intake.MaxPacket = 10 }, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := Default()
			if tc.mutate != nil {
				tc.mutate(&c)
			}
			err := c.Validate()
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}

func TestEnvOverrideRejectsNonNumber(t *testing.T) {
	c := Default()
	err := c.applyEnv(func(k string) (string, bool) {
		if k == "HM_INTAKE_READERS" {
			return "many", true
		}
		return "", false
	})
	if err == nil {
		t.Fatal("want an error for a non-numeric reader count")
	}
}

func TestHotLimit(t *testing.T) {
	const mb = 1 << 20
	for _, c := range []struct {
		setting   int
		container int64
		want      int64
		source    string
	}{
		{setting: 300, container: 4096 * mb, want: 300 * mb, source: "config"},
		{setting: -1, container: 4096 * mb, want: 0, source: "off"},
		{setting: 0, container: 512 * mb, want: 154 * mb, source: "fitted to the container limit"},
		{setting: 0, container: 1024 * mb, want: 364 * mb, source: "fitted to the container limit"},
		{setting: 0, container: 128 * mb, want: 16 * mb, source: "fitted to the container limit"},
		{setting: 0, container: 0, want: DefaultHotMB * mb, source: "default"},
	} {
		got, source := State{MaxHotMB: c.setting}.HotLimit(c.container)
		if got != c.want || source != c.source {
			t.Errorf("max_hot_mb %d, container %d: got %d (%s), want %d (%s)",
				c.setting, c.container, got, source, c.want, c.source)
		}
	}
}

func TestBackupsGoNextToTheDatabaseUnlessToldOtherwise(t *testing.T) {
	d := Default().Data
	if got := d.Backups(); got != d.Dir {
		t.Errorf("backups = %q, want next to the database, %q", got, d.Dir)
	}
	d.BackupDir = "/data/archive"
	if got := d.Backups(); got != "/data/archive" {
		t.Errorf("backups = %q, want the folder asked for", got)
	}
}

func TestLoadOpenFromEnv(t *testing.T) {
	t.Setenv("HM_OPEN", "1")
	got, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !got.Auth.Open {
		t.Error("auth.open = false, want HM_OPEN=1 to turn it on")
	}

	t.Setenv("HM_OPEN", "maybe")
	if _, err := Load(filepath.Join(t.TempDir(), "absent.yaml")); err == nil {
		t.Error("HM_OPEN=maybe loaded, want an error")
	}
}
