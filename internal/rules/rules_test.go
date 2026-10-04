package rules_test

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/protocol"
	"github.com/Sznapsollo/health-monitor-nj/internal/rules"
	"github.com/Sznapsollo/health-monitor-nj/internal/state"
)

// The documented file, so its shape is what the code reads.
const example = `
latency:
  default_ms: 1000
  level: WARN
  overrides:
    - path: /api/work/day/item/search
      ms: 2500
    - prefix: /api/adminium/
      ms: 5000
    - glob: /api/**/export*
      ms: 10000
      level: INFO
    - prefix: /actuator/
      ignore: true
    - path: /checkCaptcha
      ms: 3000
      account: "42"
offline:
  after_seconds: 300
groups:
  window_seconds: 60
mute:
  - contains: "securityService.isLoggedIn"
`

func write(t *testing.T, body string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "example")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "rules.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func subject(url string, maxMS float64, extra ...string) rules.Subject {
	dims := map[string]string{"url": url}
	for i := 0; i+1 < len(extra); i += 2 {
		dims[extra[i]] = extra[i+1]
	}
	var a state.Agg
	a.Add(1, maxMS, true)
	return rules.Subject{Signal: "requests", Dims: dims, Agg: a}
}

func TestLoadExample(t *testing.T) {
	s, err := rules.Load(write(t, example))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if s.Platform != "example" {
		t.Errorf("platform = %q, want it from the directory", s.Platform)
	}
	if s.Latency.DefaultMS != 1000 || s.Latency.Level != protocol.LevelWarn {
		t.Errorf("latency = %+v", s.Latency)
	}
	if len(s.Latency.Overrides) != 5 {
		t.Fatalf("overrides = %d", len(s.Latency.Overrides))
	}
	if s.OfflineAfter().Seconds() != 300 {
		t.Errorf("offline = %v", s.OfflineAfter())
	}
	if s.GroupWindow().Seconds() != 60 {
		t.Errorf("group window = %v", s.GroupWindow())
	}
}

func TestMissingFileGivesDefaults(t *testing.T) {
	s, err := rules.Load(filepath.Join(t.TempDir(), "acme", "rules.yaml"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if s.Latency.DefaultMS != rules.DefaultLatencyMS {
		t.Errorf("default = %v, want the built-in", s.Latency.DefaultMS)
	}
	// Deliberately not the old server's 300 ms, which alerted on nearly
	// every request.
	if s.Latency.DefaultMS == 300 {
		t.Error("the default went back to the old 300 ms")
	}
}

func TestLatencyDecisions(t *testing.T) {
	s, err := rules.Load(write(t, example))
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		subject   rules.Subject
		wantAlert bool
		wantLevel protocol.Level
		wantRule  string
	}{
		{
			name:      "under the general threshold",
			subject:   subject("/api/anything", 900),
			wantAlert: false, wantRule: "latency/default",
		},
		{
			name:      "over the general threshold",
			subject:   subject("/api/anything", 1200),
			wantAlert: true, wantLevel: protocol.LevelWarn, wantRule: "latency/default",
		},
		{
			name:      "an exact path is allowed to be slower",
			subject:   subject("/api/work/day/item/search", 2000),
			wantAlert: false, wantRule: "latency/path=/api/work/day/item/search",
		},
		{
			name:      "and still alerts past its own limit",
			subject:   subject("/api/work/day/item/search", 3000),
			wantAlert: true, wantLevel: protocol.LevelWarn,
		},
		{
			name:      "a prefix covers everything under it",
			subject:   subject("/api/adminium/reports/monthly", 4000),
			wantAlert: false, wantRule: "latency/prefix=/api/adminium/",
		},
		{
			name:      "a glob spans segments and can lower the level",
			subject:   subject("/api/reports/2026/export.csv", 11000),
			wantAlert: true, wantLevel: protocol.LevelInfo,
		},
		{
			name:      "an ignore rule says nothing at all",
			subject:   subject("/actuator/health", 99000),
			wantAlert: false, wantRule: "latency/prefix=/actuator/",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := s.EvaluateLatency(tc.subject)
			if got.Alert != tc.wantAlert {
				t.Fatalf("alert = %v, want %v (decision %+v)", got.Alert, tc.wantAlert, got)
			}
			if tc.wantLevel != "" && got.Level != tc.wantLevel {
				t.Errorf("level = %q, want %q", got.Level, tc.wantLevel)
			}
			if tc.wantRule != "" && got.Matched != tc.wantRule {
				t.Errorf("matched = %q, want %q", got.Matched, tc.wantRule)
			}
		})
	}
}

func TestMostSpecificPrefixWins(t *testing.T) {
	body := `
latency:
  default_ms: 1000
  overrides:
    - prefix: /api/
      ms: 2000
    - prefix: /api/slow/
      ms: 9000
`
	s, err := rules.Load(write(t, body))
	if err != nil {
		t.Fatal(err)
	}
	// The longer prefix wins regardless of the order in the file.
	if got := s.EvaluateLatency(subject("/api/slow/export", 5000)); got.Alert {
		t.Errorf("the more specific prefix did not win: %+v", got)
	}
	if got := s.EvaluateLatency(subject("/api/other", 5000)); !got.Alert {
		t.Errorf("the general prefix should still apply: %+v", got)
	}
}

func TestExtraMatchKeysNarrowARule(t *testing.T) {
	s, err := rules.Load(write(t, example))
	if err != nil {
		t.Fatal(err)
	}
	// The /checkCaptcha rule only applies to account 42.
	if got := s.EvaluateLatency(subject("/checkCaptcha", 2000, "account", "42")); got.Alert {
		t.Errorf("account 42 should be allowed 3000 ms: %+v", got)
	}
	if got := s.EvaluateLatency(subject("/checkCaptcha", 2000, "account", "99")); !got.Alert {
		t.Errorf("another account should fall back to the 1000 ms default: %+v", got)
	}
}

func TestSignalsWithoutADurationSaySilent(t *testing.T) {
	s := rules.Default("acme")
	var a state.Agg
	a.Add(5, 0, false)
	got := s.EvaluateLatency(rules.Subject{Signal: "jobs", Dims: map[string]string{"url": "/x"}, Agg: a})
	if got.Alert {
		t.Errorf("a countless signal raised a latency alert: %+v", got)
	}
}

func TestMute(t *testing.T) {
	s, err := rules.Load(write(t, example))
	if err != nil {
		t.Fatal(err)
	}
	if !s.Muted("securityService.isLoggedIn returned false", protocol.LevelWarn) {
		t.Error("the muted pattern was not matched")
	}
	if s.Muted("something else entirely", protocol.LevelWarn) {
		t.Error("an unrelated message was muted")
	}
}

func TestValidationRejectsUnusableRules(t *testing.T) {
	tests := []struct {
		name, body, want string
	}{
		{
			name: "no matcher",
			body: "latency:\n  overrides:\n    - ms: 100\n",
			want: "matches nothing",
		},
		{
			name: "two matchers",
			body: "latency:\n  overrides:\n    - path: /a\n      prefix: /b\n      ms: 100\n",
			want: "more than one",
		},
		{
			name: "no threshold",
			body: "latency:\n  overrides:\n    - path: /a\n",
			want: "no threshold",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := rules.Load(write(t, tc.body))
			if err == nil {
				t.Fatal("want an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestSaveRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "acme", "rules.yaml")
	s := rules.Default("acme")
	s.Latency.DefaultMS = 1500
	s.Latency.Overrides = []rules.Override{
		{Prefix: "/api/slow/", MS: 8000, Level: protocol.LevelInfo},
		{Prefix: "/actuator/", Ignore: true},
	}
	s.Mute = []rules.MutePattern{{Contains: "noise"}}

	if err := rules.Save(path, s); err != nil {
		t.Fatalf("save: %v", err)
	}
	back, err := rules.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if back.Latency.DefaultMS != 1500 || len(back.Latency.Overrides) != 2 {
		t.Fatalf("round trip lost rules: %+v", back.Latency)
	}
	if !back.Muted("some noise here", protocol.LevelWarn) {
		t.Error("round trip lost the mute pattern")
	}
	if got := back.EvaluateLatency(subject("/actuator/health", 99999)); !got.Ignored {
		t.Errorf("round trip lost the ignore: %+v", got)
	}
}

func TestAnUncompiledSetIsSafeToShare(t *testing.T) {
	s := &rules.Set{Latency: rules.Latency{DefaultMS: 100, Overrides: []rules.Override{{Prefix: "/api", MS: 50}}}}
	var a state.Agg
	a.Add(1, 70, true)
	subject := rules.Subject{Signal: "requests", Dims: map[string]string{"url": "/api/x"}, Agg: a}

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if d := s.EvaluateLatency(subject); !d.Alert || d.ThresholdMS != 50 {
				t.Errorf("decision = %+v, want the /api prefix rule applied", d)
			}
		}()
	}
	wg.Wait()
}

func TestRemindersDefaultToFiveMinutesAndCanBeOff(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rules.yaml")
	if err := os.WriteFile(path, []byte("offline:\n  after_seconds: 120\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := rules.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.RepeatEvery() != 5*time.Minute {
		t.Errorf("repeat = %v, want 5 minutes when the file says nothing", s.RepeatEvery())
	}

	s.Offline.RepeatSeconds = 0
	if err := rules.Save(path, s); err != nil {
		t.Fatal(err)
	}
	again, err := rules.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if again.RepeatEvery() != 0 || again.Offline.AfterSeconds != 120 {
		t.Errorf("after saving 0: repeat %v, after %d; want reminders off and the rest kept", again.RepeatEvery(), again.Offline.AfterSeconds)
	}
}
