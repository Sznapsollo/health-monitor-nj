package core

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/alert"
	"github.com/Sznapsollo/health-monitor-nj/internal/gauge"
	"github.com/Sznapsollo/health-monitor-nj/internal/info"
	"github.com/Sznapsollo/health-monitor-nj/internal/protocol"
	"github.com/Sznapsollo/health-monitor-nj/internal/rules"
	"github.com/Sznapsollo/health-monitor-nj/internal/signal"
	"github.com/Sznapsollo/health-monitor-nj/internal/silence"
	"github.com/Sznapsollo/health-monitor-nj/internal/state"
	"github.com/Sznapsollo/health-monitor-nj/internal/status"
)

// Alerting is everything the core needs to raise, suppress and remember
// alerts. All of it is optional: without it the server still charts traffic.
type Alerting struct {
	Alerts   *alert.Store
	Silences *silence.Store
	Statuses *status.Store
	Gauges   *gauge.Store
	Info     *info.Store
}

// SetAlerting attaches the alerting machinery.
func (c *Core) SetAlerting(a Alerting) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.alerting = a
}

// SetRules installs one platform's rules, replacing what was there.
func (c *Core) SetRules(s *rules.Set) {
	if s == nil {
		return
	}
	// Whoever built this set may have changed it field by field, so the
	// matcher is rebuilt here rather than trusted.
	s.Compile()

	c.rulesMu.Lock()
	defer c.rulesMu.Unlock()
	if c.rules == nil {
		c.rules = map[string]*rules.Set{}
	}
	c.rules[s.Platform] = s
	if c.alerting.Alerts != nil {
		c.alerting.Alerts.SetGroupWindow(s.GroupWindow())
	}
}

// Rules returns a platform's rules, falling back to the defaults so the
// caller never has to check for nil.
func (c *Core) Rules(platform string) *rules.Set {
	c.rulesMu.RLock()
	s, ok := c.rules[platform]
	if !ok {
		s, ok = c.defaults[platform]
	}
	c.rulesMu.RUnlock()
	if ok {
		return s
	}
	// Built once per platform: it is asked for every sample that is checked.
	d := rules.Default(platform)
	c.rulesMu.Lock()
	defer c.rulesMu.Unlock()
	if c.defaults == nil {
		c.defaults = map[string]*rules.Set{}
	}
	c.defaults[platform] = d
	return d
}

// OnAlert registers a listener for alerts as they are raised, which is how the
// hub pushes them to browsers.
func (c *Core) OnAlert(fn func(alert.Alert, bool)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onAlert = append(c.onAlert, fn)
}

// Raise records an alert, after asking the rules and the silences whether it
// should make any noise. A silenced alert is still stored and still visible;
// it simply does not shout.
func (c *Core) Raise(a alert.Alert) (alert.Alert, bool) {
	c.mu.Lock()
	alerting := c.alerting
	listeners := make([]func(alert.Alert, bool), len(c.onAlert))
	copy(listeners, c.onAlert)
	c.mu.Unlock()

	if alerting.Alerts == nil {
		return alert.Alert{}, false
	}

	// A muted pattern in the rules file is the platform's standing "never
	// tell me about this", the server-side replacement for the old
	// per-browser ignore lists.
	if c.Rules(a.Platform).Muted(a.Message, a.Level) {
		a.Silenced = true
		a.SilenceReason = mutedByRules
	}

	if !a.Silenced && alerting.Silences != nil {
		if sil, ok := alerting.Silences.MatchesMessage(a.Platform, a.Message); ok {
			a.Silenced = true
			a.SilenceReason = sil.Reason
			alerting.Silences.Suppressed(sil.ID)
		} else if sil, ok := alerting.Silences.Match(a.Platform, silenceTargets(a)...); ok {
			a.Silenced = true
			a.SilenceReason = sil.Reason
			alerting.Silences.Suppressed(sil.ID)
		}
	}

	stored, isNew := alerting.Alerts.Add(a)
	for _, fn := range listeners {
		fn(stored, isNew)
	}
	return stored, isNew
}

// ReapplySilences brings a platform's live alerts in line with the silences
// in force now: alerts raised before a silence are hidden by it, and alerts
// whose silence was removed or ran out show again. It returns how many changed.
func (c *Core) ReapplySilences(platform string) int {
	c.mu.Lock()
	alerting := c.alerting
	listeners := make([]func(alert.Alert, bool), len(c.onAlert))
	copy(listeners, c.onAlert)
	c.mu.Unlock()
	if alerting.Alerts == nil {
		return 0
	}
	set := c.Rules(platform)
	newlyBy := map[string]string{}
	changed := alerting.Alerts.Resilence(platform, func(a alert.Alert) (bool, string) {
		if set.Muted(a.Message, a.Level) {
			return true, mutedByRules
		}
		if alerting.Silences == nil {
			return false, ""
		}
		sil, ok := alerting.Silences.MatchesMessage(a.Platform, a.Message)
		if !ok {
			sil, ok = alerting.Silences.Match(a.Platform, silenceTargets(a)...)
		}
		if ok && !a.Silenced {
			newlyBy[a.ID] = sil.ID
		}
		return ok, sil.Reason
	})
	for _, a := range changed {
		if id, ok := newlyBy[a.ID]; ok && a.Silenced {
			alerting.Silences.Suppressed(id)
		}
		for _, fn := range listeners {
			fn(a, false)
		}
	}
	return len(changed)
}

const mutedByRules = "muted by the platform's rules"

func silenceTargets(a alert.Alert) []string {
	targets := []string{}
	if a.Target != "" {
		targets = append(targets, a.Target)
	}
	if rule, ok := a.Data["rule"].(string); ok && rule != "" {
		targets = append(targets, "rule:"+rule)
	}
	if a.Category != "" {
		targets = append(targets, "category:"+a.Category)
	}
	if a.Signal != "" {
		targets = append(targets, "signal:"+a.Signal)
	}
	if a.Platform != "" {
		targets = append(targets, "platform:"+a.Platform)
	}
	return targets
}

// evaluateSamples applies the latency rules to a flush. It runs once per
// aggregated batch rather than once per packet, so the cost does not scale
// with traffic.
func (c *Core) evaluateSamples(samples []state.Sample) {
	c.mu.Lock()
	hasAlerts := c.alerting.Alerts != nil
	c.mu.Unlock()
	if !hasAlerts {
		return
	}

	for _, s := range samples {
		set := c.Rules(s.Platform)
		decision := set.EvaluateLatency(rules.Subject{Signal: s.Signal, Dims: s.Dims, Agg: s.Agg})
		if !decision.Alert {
			continue
		}
		path := s.Dims["url"]
		if path == "" {
			path = s.Signal
		}
		c.Raise(alert.Alert{
			Platform: s.Platform,
			Signal:   s.Signal,
			Level:    decision.Level,
			Category: "latency",
			Message: fmt.Sprintf("%s took %.0f ms (limit %.0f ms)",
				path, s.Agg.MaxMS, decision.ThresholdMS),
			// One row per path per group window, rather than one per request.
			GroupKey: "latency/" + path,
			Data: map[string]any{
				"ms":        s.Agg.MaxMS,
				"limitMs":   decision.ThresholdMS,
				"rule":      decision.Matched,
				"url":       s.Dims["url"],
				"account":   s.Dims["account"],
				"user":      s.Dims["user"],
				"requests":  s.Agg.Count,
				"avgMs":     s.Agg.AvgMS(),
				"minuteKey": s.Minute,
			},
			Last: c.now(),
		})
	}
}

// observeStatus records a heartbeat and re-arms any "until recovery" silence.
func (c *Core) observeStatus(p protocol.Packet) {
	c.mu.Lock()
	alerting := c.alerting
	c.mu.Unlock()
	if alerting.Statuses == nil {
		return
	}

	// When it arrived, not what its packet says: a sender's clock or time zone
	// must not decide when it counts as gone quiet.
	at := c.now()
	if def, ok := c.reg.Lookup(p.Platform, p.Status.Signal); ok && def.Kind == signal.KindInfo {
		c.keepInfo(alerting.Info, def, p, at)
		if def.NoStatus {
			return
		}
	}
	entity, recovered := alerting.Statuses.Observe(
		p.Platform, p.Status.Signal, p.Status.Key, p.Status.Payload, at)
	if !recovered {
		return
	}

	c.Raise(alert.Alert{
		Platform: p.Platform,
		Signal:   p.Status.Signal,
		Level:    protocol.LevelInfo,
		Category: "status",
		Message:  entity.Key + " is reporting again",
		GroupKey: "status/" + entity.Key + "/recovered",
		Target:   entity.Target(),
		Data:     map[string]any{"key": entity.Key, "signal": entity.Signal},
		Last:     c.now(),
	})
}

// keepInfo stores the whole report of an info signal.
func (c *Core) keepInfo(store *info.Store, def *signal.Definition, p protocol.Packet, at time.Time) {
	if store == nil {
		return
	}
	content := p.Status.Content
	if content == nil {
		content = p.Status.Payload
	}
	record := store.Record
	if def.Merge {
		record = store.Merge
	}
	if err := record(context.Background(), p.Platform, def.Name, p.Status.Key, content, at, def.Retention.Versions); err != nil {
		c.log.Warn("info report not kept", "signal", def.Name, "key", p.Status.Key, "err", err)
	}
}

// observeGauge records the latest values one source reports.
func (c *Core) observeGauge(p protocol.Packet) {
	c.mu.Lock()
	gauges := c.alerting.Gauges
	c.mu.Unlock()
	if gauges == nil {
		return
	}
	points := make([]gauge.Point, 0, len(p.Gauge.Points))
	for _, in := range p.Gauge.Points {
		points = append(points, gauge.Point{
			Label: in.Label, Value: in.Value, Warn: in.Warn, Unit: in.Unit,
		})
	}
	gauges.Observe(p.Platform, p.Gauge.Signal, p.Source, points, c.now())
}

// CheckOffline raises an alert for anything that has gone quiet for longer
// than its platform allows.
func (c *Core) CheckOffline() {
	c.mu.Lock()
	alerting := c.alerting
	c.mu.Unlock()
	if alerting.Statuses == nil {
		return
	}

	offlineAfter := func(platform string) time.Duration {
		return c.Rules(platform).OfflineAfter()
	}
	for _, e := range alerting.Statuses.CheckOffline(offlineAfter) {
		after := offlineAfter(e.Platform)
		c.Raise(alert.Alert{
			Platform: e.Platform,
			Signal:   e.Signal,
			Level:    protocol.LevelError,
			Category: "status",
			Message:  e.Key + " has not reported for " + after.String(),
			GroupKey: "status/" + e.Key + "/offline",
			Target:   e.Target(),
			Data: map[string]any{
				"key": e.Key, "signal": e.Signal, "lastSeen": e.LastSeen,
			},
			Last: c.now(),
		})
	}
	for _, platform := range c.reg.Platforms() {
		c.remindOffline(alerting, platform)
	}
	c.sendReminders()
}

// remindOffline alerts again, on the same row, about what has stayed quiet: once
// per repeat interval since it went down, until it reports.
func (c *Core) remindOffline(alerting Alerting, platform string) {
	repeat := c.Rules(platform).RepeatEvery()
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.reminded == nil {
		c.reminded = map[string]int{}
	}
	for _, e := range alerting.Statuses.List(platform) {
		key := platform + "\x00" + e.Target()
		if !e.Offline || repeat <= 0 {
			delete(c.reminded, key)
			continue
		}
		step := int(now.Sub(e.OfflineSince) / repeat)
		if step < 1 || step <= c.reminded[key] {
			continue
		}
		c.reminded[key] = step
		c.pendingReminders = append(c.pendingReminders, reminder{
			platform: platform, entity: e, quiet: now.Sub(e.LastSeen),
		})
	}
}

type reminder struct {
	platform string
	entity   status.Entity
	quiet    time.Duration
}

// sendReminders raises what remindOffline queued, outside the core's lock.
func (c *Core) sendReminders() {
	c.mu.Lock()
	queued := c.pendingReminders
	c.pendingReminders = nil
	alerting := c.alerting
	listeners := make([]func(alert.Alert, bool), len(c.onAlert))
	copy(listeners, c.onAlert)
	c.mu.Unlock()
	for _, r := range queued {
		e := r.entity
		message := e.Key + " has not reported for " + quietFor(r.quiet)
		stored, ok := alerting.Alerts.Remind(r.platform, "status/"+e.Key+"/offline", message)
		if !ok {
			c.Raise(alert.Alert{
				Platform: r.platform, Signal: e.Signal, Level: protocol.LevelError, Category: "status",
				Message: message, GroupKey: "status/" + e.Key + "/offline", Target: e.Target(),
				Data: map[string]any{"key": e.Key, "signal": e.Signal, "lastSeen": e.LastSeen},
				Last: c.now(),
			})
			continue
		}
		for _, fn := range listeners {
			fn(stored, true)
		}
	}
}

// quietFor says how long something has been silent, to the minute: "25m",
// "1h5m".
func quietFor(d time.Duration) string {
	minutes := int(d.Round(time.Minute) / time.Minute)
	if minutes < 60 {
		return fmt.Sprintf("%dm", minutes)
	}
	return fmt.Sprintf("%dh%dm", minutes/60, minutes%60)
}

// CheckOfflineEvery runs the offline check until ctx is cancelled. Ten seconds
// matches the old server's periodic check.
func (c *Core) CheckOfflineEvery(ctx context.Context, every time.Duration) {
	if every <= 0 {
		every = 10 * time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.CheckOffline()
			for _, platform := range c.reg.Platforms() {
				c.ReapplySilences(platform)
			}
		}
	}
}

// ReviewSilences raises one low-level reminder for each indefinite mute that
// has been in force long enough to have been forgotten.
func (c *Core) ReviewSilences() {
	c.mu.Lock()
	alerting := c.alerting
	c.mu.Unlock()
	if alerting.Silences == nil {
		return
	}
	for _, sil := range alerting.Silences.NeedingReview() {
		c.Raise(alert.Alert{
			Platform: sil.Platform,
			Level:    protocol.LevelInfo,
			Category: "maintenance",
			Message: fmt.Sprintf("%q has been muted since %s (%s) and has hidden %d alerts",
				sil.Target, sil.Created.Format("2006-01-02"), sil.Reason, sil.Suppressed),
			GroupKey: "silence-review/" + sil.ID,
			Data:     map[string]any{"silenceId": sil.ID, "target": sil.Target},
			Last:     c.now(),
		})
	}
}

func (c *Core) ReviewSilencesEvery(ctx context.Context, first, every time.Duration) {
	if every <= 0 {
		every = 24 * time.Hour
	}
	if first <= 0 {
		first = every
	}
	t := time.NewTimer(first)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.ReviewSilences()
			t.Reset(every)
		}
	}
}

// alertingFields is what the core holds for alerting, kept in one place so the
// main struct stays readable.
type alertingFields struct {
	alerting Alerting
	logging  Logging
	// logDaysMu keeps the hourly rollover and a hand-made archive or delete
	// off the same tables at once.
	logDaysMu sync.Mutex
	rulesMu   sync.RWMutex
	rules     map[string]*rules.Set
	defaults  map[string]*rules.Set
	onAlert   []func(alert.Alert, bool)
}
