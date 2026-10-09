package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Sznapsollo/health-monitor-nj/internal/auth"
)

type sessionKey struct{}

// Per remote address. X-Forwarded-For is not trusted, so behind a proxy every
// client shares the proxy's bucket.
const (
	loginFailures = 10
	loginWindow   = time.Minute
	loginClients  = 4096
	maxLoginBody  = 4 << 10
	maxNameLength = 64
)

type loginLimiter struct {
	mu       sync.Mutex
	failures map[string][]time.Time
	now      func() time.Time
}

func newLoginLimiter(now func() time.Time) *loginLimiter {
	if now == nil {
		now = time.Now
	}
	return &loginLimiter{failures: map[string][]time.Time{}, now: now}
}

func (l *loginLimiter) blocked(client string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	recent := l.trim(client)
	if len(recent) < loginFailures {
		return false, 0
	}
	return true, recent[0].Add(loginWindow).Sub(l.now())
}

func (l *loginLimiter) failed(client string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	recent := l.trim(client)
	if recent == nil && len(l.failures) >= loginClients {
		l.evict()
	}
	l.failures[client] = append(recent, l.now())
}

func (l *loginLimiter) trim(client string) []time.Time {
	cutoff := l.now().Add(-loginWindow)
	kept := l.failures[client][:0]
	for _, t := range l.failures[client] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		delete(l.failures, client)
		return nil
	}
	l.failures[client] = kept
	return kept
}

func (l *loginLimiter) evict() {
	for client := range l.failures {
		l.trim(client)
	}
	if len(l.failures) < loginClients {
		return
	}
	oldest, at := "", time.Time{}
	for client, times := range l.failures {
		if last := times[len(times)-1]; oldest == "" || last.Before(at) {
			oldest, at = client, last
		}
	}
	delete(l.failures, oldest)
}

func clientAddr(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// SessionOf returns who is asking, if anyone.
func SessionOf(r *http.Request) (auth.Session, bool) {
	s, ok := r.Context().Value(sessionKey{}).(auth.Session)
	return s, ok
}

// openPaths are reachable without logging in: liveness and metrics for
// whatever watches the monitor, the login endpoint itself, and the SPA, which
// has to load before it can show a login form.
func isOpen(path string) bool {
	switch path {
	case "/healthz", "/api/version", "/api/metrics", "/api/login", "/api/session":
		return true
	}
	return !strings.HasPrefix(path, "/api/") && path != "/ws"
}

// displayAllowed is what a wall display may read. It may watch everything the
// charts need and nothing else: no actions, no rules, no silences, and not the
// search panel.
func displayAllowed(method, path string) bool {
	if method != http.MethodGet {
		return false
	}
	switch path {
	case "/ws", "/api/catalogue", "/api/series", "/api/alerts", "/api/server",
		"/api/status", "/api/gauges", "/api/dashboards", "/api/state",
		// Reading the silences is how a screen knows not to shout about an
		// alert someone has already dealt with. Creating one stays refused:
		// only GET reaches here.
		"/api/silences":
		return true
	}
	return false
}

// authenticate resolves a session from the cookie or a display token and
// enforces what that session may do.
func (d Deps) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if d.Auth == nil {
			next.ServeHTTP(w, r)
			return
		}

		session, found := d.sessionFor(r)
		if found {
			r = r.WithContext(context.WithValue(r.Context(), sessionKey{}, session))
		}

		if isOpen(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if !d.Auth.Enabled() {
			// No password configured: development, and the server says so at
			// start.
			next.ServeHTTP(w, r)
			return
		}
		if !found {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "please log in"})
			return
		}
		if session.ReadOnly() && !displayAllowed(r.Method, r.URL.Path) {
			writeJSON(w, http.StatusForbidden, map[string]string{
				"error": "this display token may only watch",
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

const maxRequestBody = 1 << 20

// guardWrites refuses a change that a browser was tricked into sending from
// another site, and anything posing as JSON that is not.
func (d Deps) guardWrites(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		if !d.sameSite(r) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "requests from another site are refused"})
			d.Log.Warn("cross-site request refused", "method", r.Method, "path", r.URL.Path,
				"origin", r.Header.Get("Origin"), "remote", r.RemoteAddr)
			return
		}
		if r.ContentLength != 0 {
			if mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mediaType != "application/json" {
				writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "send this as application/json"})
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
		}
		next.ServeHTTP(w, r)
	})
}

func (d Deps) sameSite(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin != "" && d.allowedOrigin(origin) {
		return true
	}
	switch r.Header.Get("Sec-Fetch-Site") {
	case "cross-site", "same-site":
		return false
	}
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host != "" && strings.EqualFold(u.Host, r.Host)
}

// allowedOrigin matches the configured WebSocket origins the way the
// WebSocket library does, so one setting covers both.
func (d Deps) allowedOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	for _, pattern := range d.WSOrigins {
		target := u.Host
		if strings.Contains(pattern, "://") {
			target = u.Scheme + "://" + u.Host
		}
		if ok, _ := path.Match(strings.ToLower(pattern), strings.ToLower(target)); ok {
			return true
		}
	}
	return false
}

// sessionFor reads the cookie, then a display token from the query or an
// Authorization header, so a TV can be pointed at a URL and left alone.
func (d Deps) sessionFor(r *http.Request) (auth.Session, bool) {
	if cookie, err := r.Cookie(cookieName(r)); err == nil {
		if s, ok := d.Auth.Verify(cookie.Value); ok {
			return s, true
		}
	}
	token := r.URL.Query().Get("token")
	if token == "" {
		if header := r.Header.Get("Authorization"); strings.HasPrefix(header, "Bearer ") {
			token = strings.TrimPrefix(header, "Bearer ")
		}
	}
	if token != "" {
		if s, ok := d.Auth.VerifyDisplayToken(token); ok {
			return s, true
		}
	}
	return auth.Session{}, false
}

// cookieName carries the port because browsers share cookies across ports of
// one host, so two monitors on localhost would otherwise log each other out.
func cookieName(r *http.Request) string {
	if _, port, err := net.SplitHostPort(r.Host); err == nil && port != "" {
		return auth.CookieName + "_" + port
	}
	return auth.CookieName
}

// handleLogin answers a name and the shared password with a signed cookie.
func (d Deps) handleLogin(w http.ResponseWriter, r *http.Request) {
	if d.Auth == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "no session layer"})
		return
	}
	var body struct {
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxLoginBody)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "could not read that request"})
		return
	}
	if utf8.RuneCountInString(body.Name) > maxNameLength {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("a name may be at most %d characters", maxNameLength),
		})
		return
	}
	client := clientAddr(r)
	if d.logins != nil {
		if blocked, wait := d.logins.blocked(client); blocked {
			w.Header().Set("Retry-After", strconv.Itoa(int(wait/time.Second)+1))
			writeJSON(w, http.StatusTooManyRequests, map[string]string{
				"error": "too many failed logins; try again in a minute",
			})
			d.Log.Warn("login refused: too many failures", "remote", r.RemoteAddr)
			return
		}
	}

	value, session, err := d.Auth.Login(body.Name, body.Password)
	if err != nil {
		if d.logins != nil {
			d.logins.failed(client)
		}
		// Deliberately vague, and the same for every failure.
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "that did not work"})
		d.Log.Warn("failed login", "remote", r.RemoteAddr)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     cookieName(r),
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		// Secure is set when the request arrived over TLS; behind nginx that
		// is what the forwarded proto says.
		Secure:   isTLS(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(d.Auth.TTL() / time.Second),
	})
	d.Log.Info("login", "name", session.Name, "remote", r.RemoteAddr)
	writeJSON(w, http.StatusOK, map[string]any{
		"name": session.Name, "kind": session.Kind, "expires": session.Expires,
		"defaultPassword": d.Auth.UsesDefaultPassword(),
	})
}

// handleLogout clears the cookie.
func (d Deps) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name: cookieName(r), Value: "", Path: "/", HttpOnly: true,
		Secure: isTLS(r), SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "logged out"})
}

// handleSession tells the browser who it is and whether a password is even
// required, so the SPA knows whether to show a login form.
func (d Deps) handleSession(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"required": false, "authenticated": true, "readOnly": false}
	if d.Auth != nil {
		out["required"] = d.Auth.Enabled()
		session, ok := SessionOf(r)
		out["authenticated"] = ok || !d.Auth.Enabled()
		if ok {
			out["name"] = session.Name
			out["kind"] = session.Kind
			out["readOnly"] = session.ReadOnly()
			out["expires"] = session.Expires
			// Only someone already in learns that the password is the shipped one.
			out["defaultPassword"] = !session.ReadOnly() && d.Auth.UsesDefaultPassword()
			// What this screen was paired with, so it needs nothing in its URL.
			if session.Dashboard != "" {
				out["platform"] = session.Platform
				out["dashboard"] = session.Dashboard
			}
			if session.ReadOnly() {
				out["display"] = session.Display
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// checkPairing reports whether a screen can be pointed at this dashboard.
func (d Deps) checkPairing(platform, dashboard string) error {
	if strings.TrimSpace(platform) == "" || strings.TrimSpace(dashboard) == "" {
		return errors.New("a wall display needs a platform and a dashboard to show")
	}
	if d.Dashboards == nil {
		return errors.New("this server defines no dashboards")
	}
	available := d.Dashboards.For(platform)
	for _, board := range available {
		if board.ID == dashboard {
			return nil
		}
	}
	names := make([]string, 0, len(available))
	for _, board := range available {
		names = append(names, board.ID)
	}
	if len(names) == 0 {
		return fmt.Errorf("%s defines no dashboards to show", platform)
	}
	return fmt.Errorf("%s has no dashboard %q; it has %s",
		platform, dashboard, strings.Join(names, ", "))
}

// handleDisplayTokens lists the wall-display credentials.
func (d Deps) handleDisplayTokens(w http.ResponseWriter, r *http.Request) {
	if d.Auth == nil {
		writeJSON(w, http.StatusOK, map[string]any{"tokens": []any{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": d.Auth.DisplayTokens()})
}

// handleCreateDisplayToken makes one. The secret is shown exactly once.
func (d Deps) handleCreateDisplayToken(w http.ResponseWriter, r *http.Request) {
	if d.Auth == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "no session layer"})
		return
	}
	var body struct {
		Name      string              `json:"name"`
		Platform  string              `json:"platform"`
		Dashboard string              `json:"dashboard"`
		Options   auth.DisplayOptions `json:"options"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "could not read that request"})
		return
	}
	// A screen shows an arrangement the team already keeps in
	// platforms/<platform>/dashboards.yaml, so the pairing is checked here
	// rather than discovered as a blank wall later.
	if err := d.checkPairing(body.Platform, body.Dashboard); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	token, err := d.Auth.CreateDisplayToken(r.Context(), auth.NewDisplayToken{
		Name:      body.Name,
		Platform:  body.Platform,
		Dashboard: body.Dashboard,
		Options:   body.Options,
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	d.Log.Info("display token created", "name", token.Name, "id", token.ID)
	writeJSON(w, http.StatusCreated, token)
}

// handleDisplayOptions changes how a screen draws itself, keeping its token.
func (d Deps) handleDisplayOptions(w http.ResponseWriter, r *http.Request) {
	if d.Auth == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "no session layer"})
		return
	}
	var body auth.DisplayOptions
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "could not read that request"})
		return
	}
	token, err := d.Auth.SetDisplayOptions(r.Context(), r.PathValue("id"), body)
	if errors.Is(err, auth.ErrNoToken) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	d.Log.Info("display options changed", "id", token.ID, "by", sessionName(r))
	writeJSON(w, http.StatusOK, token)
}

// handleRevokeDisplayToken stops one working.
func (d Deps) handleRevokeDisplayToken(w http.ResponseWriter, r *http.Request) {
	if d.Auth == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "no session layer"})
		return
	}
	id := r.PathValue("id")
	if err := d.Auth.RevokeDisplayToken(r.Context(), id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	d.Log.Info("display token revoked", "id", id)
	writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}

func isTLS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}
