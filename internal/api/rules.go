package api

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"sort"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/protocol"
	"github.com/Sznapsollo/health-monitor-nj/internal/rules"
	"github.com/Sznapsollo/health-monitor-nj/internal/state"
)

// rulesBody is the shape the Settings panel reads and writes.
type rulesBody struct {
	Platform string           `json:"platform"`
	Latency  latencyBody      `json:"latency"`
	Offline  offlineBody      `json:"offline"`
	Groups   groupsBody       `json:"groups"`
	Mute     []mutePatternDTO `json:"mute,omitempty"`
}

type latencyBody struct {
	DefaultMS float64       `json:"defaultMs"`
	Level     string        `json:"level"`
	Overrides []overrideDTO `json:"overrides,omitempty"`
}

type overrideDTO struct {
	Path       string  `json:"path,omitempty"`
	Prefix     string  `json:"prefix,omitempty"`
	Glob       string  `json:"glob,omitempty"`
	MS         float64 `json:"ms,omitempty"`
	Level      string  `json:"level,omitempty"`
	Ignore     bool    `json:"ignore,omitempty"`
	Account    string  `json:"account,omitempty"`
	User       string  `json:"user,omitempty"`
	ServerName string  `json:"serverName,omitempty"`
}

type offlineBody struct {
	AfterSeconds int `json:"afterSeconds"`
	// RepeatSeconds absent keeps the default; 0 alerts once.
	RepeatSeconds *int `json:"repeatSeconds,omitempty"`
}

type groupsBody struct {
	WindowSeconds int `json:"windowSeconds"`
}

type mutePatternDTO struct {
	Contains string `json:"contains"`
	Level    string `json:"level,omitempty"`
}

func toBody(s *rules.Set) rulesBody {
	b := rulesBody{
		Platform: s.Platform,
		Latency:  latencyBody{DefaultMS: s.Latency.DefaultMS, Level: string(s.Latency.Level)},
		Offline:  offlineBody{AfterSeconds: s.Offline.AfterSeconds, RepeatSeconds: &s.Offline.RepeatSeconds},
		Groups:   groupsBody{WindowSeconds: s.Groups.WindowSeconds},
	}
	for _, o := range s.Latency.Overrides {
		b.Latency.Overrides = append(b.Latency.Overrides, overrideDTO{
			Path: o.Path, Prefix: o.Prefix, Glob: o.Glob, MS: o.MS,
			Level: string(o.Level), Ignore: o.Ignore,
			Account: o.Account, User: o.User, ServerName: o.ServerName,
		})
	}
	for _, m := range s.Mute {
		b.Mute = append(b.Mute, mutePatternDTO{Contains: m.Contains, Level: string(m.Level)})
	}
	return b
}

func fromBody(b rulesBody, platform string) *rules.Set {
	if b.Platform == "" {
		b.Platform = platform
	}
	s := rules.Default(b.Platform)
	s.Latency.DefaultMS = b.Latency.DefaultMS
	if b.Latency.Level != "" {
		s.Latency.Level = rules.ParseLevel(b.Latency.Level, protocol.LevelWarn)
	}
	s.Latency.Overrides = nil
	for _, o := range b.Latency.Overrides {
		s.Latency.Overrides = append(s.Latency.Overrides, rules.Override{
			Path: o.Path, Prefix: o.Prefix, Glob: o.Glob, MS: o.MS,
			Level: rules.ParseLevel(o.Level, ""), Ignore: o.Ignore,
			Account: o.Account, User: o.User, ServerName: o.ServerName,
		})
	}
	if b.Offline.AfterSeconds > 0 {
		s.Offline.AfterSeconds = b.Offline.AfterSeconds
	}
	if b.Offline.RepeatSeconds != nil {
		s.Offline.RepeatSeconds = max(0, *b.Offline.RepeatSeconds)
	}
	if b.Groups.WindowSeconds > 0 {
		s.Groups.WindowSeconds = b.Groups.WindowSeconds
	}
	s.Mute = nil
	for _, m := range b.Mute {
		s.Mute = append(s.Mute, rules.MutePattern{Contains: m.Contains, Level: rules.ParseLevel(m.Level, "")})
	}
	s.Compile()
	return s
}

// handleGetRules returns a platform's alert rules.
func (d Deps) handleGetRules(w http.ResponseWriter, r *http.Request) {
	if d.Core == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "rules are not available"})
		return
	}
	platform := d.platformOf(r)
	writeJSON(w, http.StatusOK, toBody(d.Core.Rules(platform)))
}

// handlePutRules saves a platform's rules. The file watcher would pick the
// write up on its own; applying it here as well means the answer to this
// request is already the truth.
func (d Deps) handlePutRules(w http.ResponseWriter, r *http.Request) {
	if d.Core == nil || d.PlatformsDir == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "rules cannot be saved here"})
		return
	}
	var body rulesBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "could not read those rules"})
		return
	}
	platform := d.platformOf(r)
	set := fromBody(body, platform)
	if err := set.Validate(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if !d.knownPlatform(set.Platform) {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "no platform " + set.Platform + " is declared under " + d.PlatformsDir,
		})
		return
	}

	path := filepath.Join(d.PlatformsDir, set.Platform, "rules.yaml")
	if err := rules.Save(path, set); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	d.Core.SetRules(set)
	d.Log.Info("alert rules saved", "platform", set.Platform, "overrides", len(set.Latency.Overrides))
	writeJSON(w, http.StatusOK, toBody(set))
}

func (d Deps) knownPlatform(platform string) bool {
	if d.Registry == nil || platform == "" {
		return false
	}
	for _, p := range d.Registry.Platforms() {
		if p == platform {
			return true
		}
	}
	return false
}

// ruleTest is what "test against the last hour" answers: how many alerts each
// rule would have produced, so a threshold can be tuned before it goes live.
type ruleTest struct {
	Platform string           `json:"platform"`
	Minutes  int              `json:"minutes"`
	Rows     int              `json:"rows"`
	Total    int              `json:"total"`
	ByRule   []ruleTestBucket `json:"byRule"`
	Examples []ruleTestSample `json:"examples"`
}

type ruleTestBucket struct {
	Rule  string `json:"rule"`
	Count int    `json:"count"`
}

type ruleTestSample struct {
	URL       string  `json:"url"`
	MS        float64 `json:"ms"`
	LimitMS   float64 `json:"limitMs"`
	Rule      string  `json:"rule"`
	Level     string  `json:"level"`
	MinuteKey int64   `json:"minute"`
	Requests  int64   `json:"requests"`
}

// handleTestRules replays a recent window through candidate rules without
// raising anything.
func (d Deps) handleTestRules(w http.ResponseWriter, r *http.Request) {
	if d.DB == nil || d.Core == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "no history to test against"})
		return
	}

	platform := d.platformOf(r)
	set := d.Core.Rules(platform)
	if r.Body != nil && r.ContentLength > 0 {
		var body rulesBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "could not read those rules"})
			return
		}
		set = fromBody(body, platform)
		if err := set.Validate(); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
	}

	minutes := atoi(r.URL.Query().Get("minutes"))
	if minutes <= 0 {
		minutes = 60
	}
	to := state.MinuteOf(time.Now())
	from := to - int64(minutes) + 1

	signals, err := d.DB.Signals(r.Context(), platform)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	out := ruleTest{Platform: platform, Minutes: minutes, ByRule: []ruleTestBucket{}, Examples: []ruleTestSample{}}
	byRule := map[string]int{}
	for _, sig := range signals {
		rows, err := d.DB.Rows(r.Context(), platform, sig, "url", from, to, 200000)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		out.Rows += len(rows)
		for _, row := range rows {
			decision := set.EvaluateLatency(rules.Subject{Signal: sig, Dims: row.Dims, Agg: row.Agg})
			if !decision.Alert {
				continue
			}
			out.Total++
			byRule[decision.Matched]++
			if len(out.Examples) < 20 {
				out.Examples = append(out.Examples, ruleTestSample{
					URL: row.Dims["url"], MS: row.Agg.MaxMS, LimitMS: decision.ThresholdMS,
					Rule: decision.Matched, Level: string(decision.Level),
					MinuteKey: row.Minute, Requests: row.Agg.Count,
				})
			}
		}
	}

	for rule, count := range byRule {
		out.ByRule = append(out.ByRule, ruleTestBucket{Rule: rule, Count: count})
	}
	sort.Slice(out.ByRule, func(i, j int) bool { return out.ByRule[i].Count > out.ByRule[j].Count })
	writeJSON(w, http.StatusOK, out)
}
