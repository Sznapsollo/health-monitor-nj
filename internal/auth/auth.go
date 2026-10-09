// Package auth is the dashboard's login: any name plus one shared password,
// answered with a signed cookie. It is deliberately small and
// kept behind one interface, so named users or an identity provider can
// replace the check later without touching the session layer.
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

// CookieName is the session cookie.
const CookieName = "hm_session"

// DefaultPassword is what a fresh install logs in with until HM_PASS is set.
const DefaultPassword = "default"

// DefaultTTL is how long a login lasts. A wall display uses a token instead,
// because a TV cannot retype a password after a reboot.
const DefaultTTL = 7 * 24 * time.Hour

// Kind says what a caller is allowed to do.
type Kind string

const (
	// KindUser is a person who logged in: everything.
	KindUser Kind = "user"
	// KindDisplay is a wall display: it may watch, and nothing else.
	KindDisplay Kind = "display"
)

// Session is who is asking.
type Session struct {
	Name    string
	Kind    Kind
	Expires time.Time
	// Platform and Dashboard are the arrangement a wall display is paired
	// with: the screen shows that and nothing else, so what it displays is
	// decided here rather than in a URL taped to the back of a TV.
	Platform  string
	Dashboard string
	// Display is how a wall display draws itself.
	Display DisplayOptions
}

// ReadOnly reports whether this session may only look.
func (s Session) ReadOnly() bool { return s.Kind == KindDisplay }

const schema = `
CREATE TABLE IF NOT EXISTS setting (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS display_token (
    id        TEXT PRIMARY KEY,
    name      TEXT    NOT NULL,
    secret    TEXT    NOT NULL,
    created   INTEGER NOT NULL,
    last_used INTEGER NOT NULL DEFAULT 0,
    platform  TEXT    NOT NULL DEFAULT '',
    dashboard TEXT    NOT NULL DEFAULT '',
    options   TEXT    NOT NULL DEFAULT '{}'
);
`

// Columns added after the first release. SQLite has no "add column if not
// exists", and a token made before the pairing existed simply has none.
var migrations = []string{
	`ALTER TABLE display_token ADD COLUMN platform TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE display_token ADD COLUMN dashboard TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE display_token ADD COLUMN options TEXT NOT NULL DEFAULT '{}'`,
}

// Manager checks credentials and signs sessions.
type Manager struct {
	db       *sql.DB
	password string
	secret   []byte
	ttl      time.Duration
	now      func() time.Time

	mu     sync.RWMutex
	tokens map[string]*DisplayToken
}

// Options configure the manager.
type Options struct {
	DB *sql.DB
	// Password is the shared dashboard password. Empty switches authentication
	// off, which is meant for development only.
	Password string
	TTL      time.Duration
	Now      func() time.Time
}

// Open prepares the tables, loads the display tokens and makes sure there is a
// signing secret. The secret is stored, so a restart does not log everyone out.
func Open(o Options) (*Manager, error) {
	if o.TTL <= 0 {
		o.TTL = DefaultTTL
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	m := &Manager{
		db: o.DB, password: o.Password, ttl: o.TTL, now: o.Now,
		tokens: map[string]*DisplayToken{},
	}
	if o.DB == nil {
		secret, err := newSecret()
		if err != nil {
			return nil, err
		}
		m.secret = signingKey(secret, o.Password)
		return m, nil
	}
	if _, err := o.DB.Exec(schema); err != nil {
		return nil, fmt.Errorf("auth: schema: %w", err)
	}
	for _, stmt := range migrations {
		// Already there is the normal case, and the only error worth ignoring.
		if _, err := o.DB.Exec(stmt); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return nil, fmt.Errorf("auth: migrate: %w", err)
		}
	}
	secret, err := m.loadOrCreateSecret()
	if err != nil {
		return nil, err
	}
	m.secret = signingKey(secret, o.Password)
	if err := m.loadTokens(); err != nil {
		return nil, err
	}
	return m, nil
}

// Enabled reports whether a password is configured. When it is not, every
// request is treated as a logged-in user and the server says so at start.
func (m *Manager) Enabled() bool { return m.password != "" }

// UsesDefaultPassword reports whether the password is still the shipped one.
func (m *Manager) UsesDefaultPassword() bool { return m.password == DefaultPassword }

// signingKey ties sessions to the password, so changing it logs everyone out.
func signingKey(secret []byte, password string) []byte {
	sum := sha256.Sum256([]byte(password))
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("session\x00"))
	mac.Write(sum[:])
	return mac.Sum(nil)
}

func newSecret() ([]byte, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("auth: no randomness: %w", err)
	}
	return secret, nil
}

func (m *Manager) loadOrCreateSecret() ([]byte, error) {
	var encoded string
	err := m.db.QueryRow(`SELECT value FROM setting WHERE key = 'session_secret'`).Scan(&encoded)
	switch {
	case err == nil:
		secret, err := base64.StdEncoding.DecodeString(encoded)
		if err == nil && len(secret) >= 32 {
			return secret, nil
		}
	case !errors.Is(err, sql.ErrNoRows):
		return nil, fmt.Errorf("auth: read secret: %w", err)
	}

	secret, err := newSecret()
	if err != nil {
		return nil, err
	}
	_, err = m.db.Exec(
		`INSERT INTO setting (key, value) VALUES ('session_secret', ?)
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`,
		base64.StdEncoding.EncodeToString(secret))
	if err != nil {
		return nil, fmt.Errorf("auth: store secret: %w", err)
	}
	return secret, nil
}

// Login checks the shared password and returns a signed cookie value. The name
// is not checked against anything: it exists so the sessions list and "send
// message" have something to show.
func (m *Manager) Login(name, password string) (string, Session, error) {
	if !m.Enabled() {
		return m.issue(name, KindUser)
	}
	// Constant time, so the answer does not depend on how much of the password
	// was right.
	if subtle.ConstantTimeCompare([]byte(password), []byte(m.password)) != 1 {
		return "", Session{}, ErrWrongPassword
	}
	if strings.TrimSpace(name) == "" {
		name = "someone"
	}
	return m.issue(name, KindUser)
}

// ErrWrongPassword is returned for a failed login.
var ErrWrongPassword = errors.New("auth: wrong password")

func (m *Manager) issue(name string, kind Kind) (string, Session, error) {
	s := Session{Name: name, Kind: kind, Expires: m.now().Add(m.ttl)}
	return m.sign(s), s, nil
}

// sign encodes a session as name|kind|expiry.signature.
func (m *Manager) sign(s Session) string {
	payload := fmt.Sprintf("%s|%s|%d",
		base64.RawURLEncoding.EncodeToString([]byte(s.Name)), s.Kind, s.Expires.Unix())
	mac := hmac.New(sha256.New, m.secret)
	mac.Write([]byte(payload))
	return payload + "." + hex.EncodeToString(mac.Sum(nil))
}

// Verify reads a cookie value back, rejecting anything tampered with, past
// its expiry, or expiring later than a login made now would.
func (m *Manager) Verify(value string) (Session, bool) {
	dot := strings.LastIndex(value, ".")
	if dot < 0 {
		return Session{}, false
	}
	payload, signature := value[:dot], value[dot+1:]

	mac := hmac.New(sha256.New, m.secret)
	mac.Write([]byte(payload))
	want := hex.EncodeToString(mac.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(signature), []byte(want)) != 1 {
		return Session{}, false
	}

	parts := strings.Split(payload, "|")
	if len(parts) != 3 {
		return Session{}, false
	}
	name, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return Session{}, false
	}
	expires, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return Session{}, false
	}
	s := Session{Name: string(name), Kind: Kind(parts[1]), Expires: time.Unix(expires, 0)}
	if now := m.now(); now.After(s.Expires) || s.Expires.After(now.Add(m.ttl+expirySlack)) {
		return Session{}, false
	}
	return s, true
}

const expirySlack = time.Minute

// TTL is how long a session lasts, for the cookie's max age.
func (m *Manager) TTL() time.Duration { return m.ttl }
