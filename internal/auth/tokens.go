package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const lastUsedEvery = time.Minute

// DisplayToken is a long-lived, read-only credential for a wall display: a TV
// cannot retype a password after a reboot, and a seven-day cookie is too short.
type DisplayToken struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Platform and Dashboard are what this screen shows. A token is paired
	// with one arrangement when it is created, so re-pointing a screen is a
	// change here rather than a trip to the wall with a keyboard.
	Platform  string         `json:"platform,omitempty"`
	Dashboard string         `json:"dashboard,omitempty"`
	Options   DisplayOptions `json:"options"`
	Created   time.Time      `json:"created"`
	LastUsed  time.Time      `json:"lastUsed,omitzero"`
	// Secret is only ever returned once, when the token is created.
	Secret string `json:"secret,omitempty"`

	hashed string
}

// DisplayOptions are how a wall display draws itself, beyond what it shows.
type DisplayOptions struct {
	// ShowSystem puts the monitor's memory, CPU and traffic in the corner.
	ShowSystem bool `json:"showSystem,omitempty"`
	// ShowLive says whether the screen is connected and when it last updated.
	ShowLive bool `json:"showLive,omitempty"`
	// Theme is "light", "dark", "screen" for a switch on the screen itself,
	// or "" to follow the screen's own system setting.
	Theme string `json:"theme,omitempty"`
}

func (o DisplayOptions) clean() DisplayOptions {
	if o.Theme != "light" && o.Theme != "dark" && o.Theme != "screen" {
		o.Theme = ""
	}
	return o
}

func (m *Manager) loadTokens() error {
	rows, err := m.db.Query(
		`SELECT id, name, secret, created, last_used, platform, dashboard, options FROM display_token`)
	if err != nil {
		return fmt.Errorf("auth: load tokens: %w", err)
	}
	defer func() { _ = rows.Close() }()

	loaded := map[string]*DisplayToken{}
	for rows.Next() {
		var (
			t                 DisplayToken
			created, lastUsed int64
			options           string
		)
		err := rows.Scan(&t.ID, &t.Name, &t.hashed, &created, &lastUsed, &t.Platform, &t.Dashboard, &options)
		if err != nil {
			return fmt.Errorf("auth: scan token: %w", err)
		}
		_ = json.Unmarshal([]byte(options), &t.Options)
		t.Created = time.UnixMilli(created)
		if lastUsed > 0 {
			t.LastUsed = time.UnixMilli(lastUsed)
		}
		loaded[t.ID] = &t
	}
	if err := rows.Err(); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.tokens = loaded
	return nil
}

// NewDisplayToken is what a screen is being set up to show.
type NewDisplayToken struct {
	Name string
	// Platform and Dashboard pair the screen with an arrangement. The caller
	// checks that the dashboard exists; this package only stores the pairing.
	Platform  string
	Dashboard string
	Options   DisplayOptions
}

// CreateDisplayToken makes a token and returns it with its secret. Only the
// hash is stored, so a leaked database does not hand out working displays.
func (m *Manager) CreateDisplayToken(ctx context.Context, spec NewDisplayToken) (DisplayToken, error) {
	name := strings.TrimSpace(spec.Name)
	if name == "" {
		return DisplayToken{}, fmt.Errorf("auth: a display token needs a name")
	}
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return DisplayToken{}, fmt.Errorf("auth: no randomness: %w", err)
	}
	secret := base64.RawURLEncoding.EncodeToString(raw)

	t := DisplayToken{
		ID:        hashToken(secret)[:12],
		Name:      name,
		Platform:  strings.TrimSpace(spec.Platform),
		Dashboard: strings.TrimSpace(spec.Dashboard),
		Options:   spec.Options.clean(),
		Created:   m.now(),
		Secret:    secret,
		hashed:    hashToken(secret),
	}
	if m.db != nil {
		options, _ := json.Marshal(t.Options)
		_, err := m.db.ExecContext(ctx,
			`INSERT INTO display_token (id, name, secret, created, last_used, platform, dashboard, options)
			 VALUES (?, ?, ?, ?, 0, ?, ?, ?)`,
			t.ID, t.Name, t.hashed, t.Created.UnixMilli(), t.Platform, t.Dashboard, string(options))
		if err != nil {
			return DisplayToken{}, fmt.Errorf("auth: store token: %w", err)
		}
	}

	m.mu.Lock()
	stored := t
	stored.Secret = ""
	m.tokens[t.ID] = &stored
	m.mu.Unlock()
	return t, nil
}

// ErrNoToken is returned for a display token that does not exist.
var ErrNoToken = errors.New("auth: no such display token")

// SetDisplayOptions changes how a display draws itself; it takes effect the
// next time the screen loads, with the same token.
func (m *Manager) SetDisplayOptions(ctx context.Context, id string, o DisplayOptions) (DisplayToken, error) {
	o = o.clean()
	m.mu.Lock()
	t, ok := m.tokens[id]
	if ok {
		t.Options = o
	}
	var out DisplayToken
	if ok {
		out = *t
	}
	m.mu.Unlock()
	if !ok {
		return DisplayToken{}, ErrNoToken
	}
	if m.db != nil {
		options, _ := json.Marshal(o)
		if _, err := m.db.ExecContext(ctx, `UPDATE display_token SET options = ? WHERE id = ?`, string(options), id); err != nil {
			return DisplayToken{}, fmt.Errorf("auth: update token: %w", err)
		}
	}
	out.Secret = ""
	return out, nil
}

// RevokeDisplayToken stops a display working, which is what happens when a
// screen is retired or a token leaks.
func (m *Manager) RevokeDisplayToken(ctx context.Context, id string) error {
	if m.db != nil {
		if _, err := m.db.ExecContext(ctx, `DELETE FROM display_token WHERE id = ?`, id); err != nil {
			return fmt.Errorf("auth: revoke token: %w", err)
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.tokens, id)
	return nil
}

// DisplayTokens lists the tokens, newest first, without their secrets.
func (m *Manager) DisplayTokens() []DisplayToken {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]DisplayToken, 0, len(m.tokens))
	for _, t := range m.tokens {
		copied := *t
		copied.Secret = ""
		out = append(out, copied)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out
}

// VerifyDisplayToken turns a token into a read-only session.
func (m *Manager) VerifyDisplayToken(secret string) (Session, bool) {
	if secret == "" {
		return Session{}, false
	}
	hashed := hashToken(secret)
	now := m.now()

	m.mu.Lock()
	var found *DisplayToken
	for _, t := range m.tokens {
		if subtleEqual(t.hashed, hashed) {
			found = t
			break
		}
	}
	record := found != nil && now.Sub(found.LastUsed) >= lastUsedEvery
	if record {
		found.LastUsed = now
	}
	var session Session
	if found != nil {
		session = Session{
			Name:      "display: " + found.Name,
			Kind:      KindDisplay,
			Expires:   now.Add(m.ttl),
			Platform:  found.Platform,
			Dashboard: found.Dashboard,
			Display:   found.Options,
		}
	}
	m.mu.Unlock()

	if found == nil {
		return Session{}, false
	}
	if record && m.db != nil {
		_, _ = m.db.Exec(`UPDATE display_token SET last_used = ? WHERE id = ?`,
			now.UnixMilli(), found.ID)
	}
	return session, true
}

func hashToken(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func subtleEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := range len(a) {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}
