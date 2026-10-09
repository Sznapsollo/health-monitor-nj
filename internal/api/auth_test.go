package api_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/api"
	"github.com/Sznapsollo/health-monitor-nj/internal/auth"
	"github.com/Sznapsollo/health-monitor-nj/internal/dashboard"
	"github.com/Sznapsollo/health-monitor-nj/internal/hub"
	"github.com/Sznapsollo/health-monitor-nj/internal/signal"
	"github.com/Sznapsollo/health-monitor-nj/internal/state"
	"github.com/Sznapsollo/health-monitor-nj/internal/store"
	"github.com/prometheus/client_golang/prometheus"
)

func authFixture(t *testing.T, password string) (*httptest.Server, *auth.Manager) {
	t.Helper()
	db, err := store.Open(store.Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	manager, err := auth.Open(auth.Options{DB: db.DB(), Password: password})
	if err != nil {
		t.Fatal(err)
	}

	reg := signal.NewRegistry()
	hot := state.NewStore(reg, func() time.Time { return now })
	boards := dashboard.NewStore()
	boards.Replace("test", []dashboard.Dashboard{{ID: "ops", Platform: "test", Name: "Ops"}})
	srv := httptest.NewServer(api.Handler(api.Deps{
		Log:        slog.New(slog.NewTextHandler(discard{}, nil)),
		Metrics:    prometheus.NewRegistry(),
		Registry:   reg,
		Store:      hot,
		DB:         db,
		Hub:        hub.New(hot, reg, func() time.Time { return now }, time.Millisecond),
		Auth:       manager,
		Dashboards: boards,
		Started:    now,
	}))
	t.Cleanup(srv.Close)
	return srv, manager
}

func client(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Jar: jar}
}

func post(t *testing.T, c *http.Client, url string, body any) *http.Response {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.Post(url, "application/json", bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestWithoutAPasswordEverythingIsOpen(t *testing.T) {
	srv, _ := authFixture(t, "")
	res, err := http.Get(srv.URL + "/api/catalogue")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want a development server to stay open", res.StatusCode)
	}
}

func TestLoginIsRequiredAndWorks(t *testing.T) {
	srv, _ := authFixture(t, "s3cret")
	c := client(t)

	// Before logging in, the API says no.
	res, err := c.Get(srv.URL + "/api/catalogue")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", res.StatusCode)
	}

	// But the SPA and liveness still load, or there would be nowhere to type
	// the password.
	for _, path := range []string{"/healthz", "/api/metrics", "/"} {
		res, err := c.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode == http.StatusUnauthorized {
			t.Errorf("%s requires a login", path)
		}
	}

	// A wrong password is refused.
	res = post(t, c, srv.URL+"/api/login", map[string]string{"name": "anna", "password": "nope"})
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong password gave %d", res.StatusCode)
	}

	// The right one lets everything through.
	res = post(t, c, srv.URL+"/api/login", map[string]string{"name": "anna", "password": "s3cret"})
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("login gave %d", res.StatusCode)
	}

	res, err = c.Get(srv.URL + "/api/catalogue")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status after login = %d", res.StatusCode)
	}

	// And the session says who it is.
	res, err = c.Get(srv.URL + "/api/session")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	var session map[string]any
	if err := json.NewDecoder(res.Body).Decode(&session); err != nil {
		t.Fatal(err)
	}
	if session["name"] != "anna" || session["readOnly"] != false {
		t.Fatalf("session = %+v", session)
	}
}

func TestLogout(t *testing.T) {
	srv, _ := authFixture(t, "s3cret")
	c := client(t)
	_ = post(t, c, srv.URL+"/api/login", map[string]string{"name": "anna", "password": "s3cret"}).Body.Close()

	_ = post(t, c, srv.URL+"/api/logout", nil).Body.Close()

	res, err := c.Get(srv.URL + "/api/catalogue")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status after logout = %d", res.StatusCode)
	}
}

func TestATamperedCookieIsRefused(t *testing.T) {
	srv, manager := authFixture(t, "s3cret")
	value, _, err := manager.Login("anna", "s3cret")
	if err != nil {
		t.Fatal(err)
	}

	for _, bad := range []string{value + "x", "rubbish", value[:len(value)-4]} {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/catalogue", nil)
		req.AddCookie(&http.Cookie{Name: auth.CookieName + "_" + req.URL.Port(), Value: bad})
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode != http.StatusUnauthorized {
			t.Errorf("a tampered cookie gave %d", res.StatusCode)
		}
	}
}

func TestDisplayTokenMayOnlyWatch(t *testing.T) {
	srv, _ := authFixture(t, "s3cret")
	c := client(t)
	_ = post(t, c, srv.URL+"/api/login", map[string]string{"name": "anna", "password": "s3cret"}).Body.Close()

	// A screen is paired with an arrangement that exists; that is what it shows.
	res := post(t, c, srv.URL+"/api/display-tokens", map[string]string{
		"name": "kitchen TV", "platform": "test", "dashboard": "ops",
	})
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create gave %d", res.StatusCode)
	}
	var token struct {
		ID        string `json:"id"`
		Secret    string `json:"secret"`
		Dashboard string `json:"dashboard"`
	}
	if err := json.NewDecoder(res.Body).Decode(&token); err != nil {
		t.Fatal(err)
	}
	if token.Secret == "" {
		t.Fatal("no secret was returned")
	}
	if token.Dashboard != "ops" {
		t.Errorf("dashboard = %q, want the arrangement the screen was paired with", token.Dashboard)
	}

	// The token can watch the charts.
	watch, err := http.Get(srv.URL + "/api/catalogue?token=" + token.Secret)
	if err != nil {
		t.Fatal(err)
	}
	_ = watch.Body.Close()
	if watch.StatusCode != http.StatusOK {
		t.Errorf("a display token could not read the catalogue: %d", watch.StatusCode)
	}

	// But it may not search, act, or change anything.
	// The alerts and status a dashboard panel draws, silences included.
	for _, path := range []string{"/api/alerts", "/api/status", "/api/silences"} {
		res, err := http.Get(srv.URL + path + "?token=" + token.Secret)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Errorf("a display could not read %s: %d", path, res.StatusCode)
		}
	}

	refused := []struct {
		method, path string
	}{
		{http.MethodGet, "/api/search"},
		{http.MethodPost, "/api/silences"},
		{http.MethodDelete, "/api/silences/1"},
		{http.MethodPut, "/api/silences/1"},
		{http.MethodPost, "/api/backup"},
		{http.MethodGet, "/api/backups"},
		{http.MethodGet, "/api/backups/hm-backup-20260920-093000.db"},
		{http.MethodDelete, "/api/backups/hm-backup-20260920-093000.db"},
		{http.MethodPut, "/api/rules"},
		{http.MethodPost, "/api/message"},
		{http.MethodGet, "/api/sessions"},
	}
	for _, tc := range refused {
		req, _ := http.NewRequest(tc.method, srv.URL+tc.path+"?token="+token.Secret, bytes.NewReader([]byte("{}")))
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode != http.StatusForbidden {
			t.Errorf("%s %s gave %d, want it refused for a display", tc.method, tc.path, res.StatusCode)
		}
	}
}

func TestRevokedTokenStopsWorking(t *testing.T) {
	srv, _ := authFixture(t, "s3cret")
	c := client(t)
	_ = post(t, c, srv.URL+"/api/login", map[string]string{"name": "anna", "password": "s3cret"}).Body.Close()

	// A screen is paired with an arrangement that exists; that is what it shows.
	res := post(t, c, srv.URL+"/api/display-tokens", map[string]string{
		"name": "kitchen TV", "platform": "test", "dashboard": "ops",
	})
	var token struct {
		ID        string `json:"id"`
		Secret    string `json:"secret"`
		Dashboard string `json:"dashboard"`
	}
	_ = json.NewDecoder(res.Body).Decode(&token)
	_ = res.Body.Close()

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/display-tokens/"+token.ID, nil)
	del, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = del.Body.Close()

	after, err := http.Get(srv.URL + "/api/catalogue?token=" + token.Secret)
	if err != nil {
		t.Fatal(err)
	}
	_ = after.Body.Close()
	if after.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a revoked token still worked: %d", after.StatusCode)
	}
}

func TestSessionsSurviveARestart(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(store.Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	first, err := auth.Open(auth.Options{DB: db.DB(), Password: "s3cret"})
	if err != nil {
		t.Fatal(err)
	}
	value, _, err := first.Login("anna", "s3cret")
	if err != nil {
		t.Fatal(err)
	}

	// A new process over the same database keeps the signing secret, so a
	// restart does not log everyone out.
	second, err := auth.Open(auth.Options{DB: db.DB(), Password: "s3cret"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := second.Verify(value); !ok {
		t.Fatal("the cookie stopped working after a restart")
	}
}

func TestSessionExpires(t *testing.T) {
	clock := now
	manager, err := auth.Open(auth.Options{
		Password: "s3cret",
		TTL:      time.Hour,
		Now:      func() time.Time { return clock },
	})
	if err != nil {
		t.Fatal(err)
	}
	value, _, err := manager.Login("anna", "s3cret")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := manager.Verify(value); !ok {
		t.Fatal("a fresh session should verify")
	}

	// The same manager, an hour and a bit later: the cookie is still
	// correctly signed, and still refused.
	clock = now.Add(time.Hour + time.Minute)
	if _, ok := manager.Verify(value); ok {
		t.Fatal("an expired session was accepted")
	}
}

func TestADisplayMustBePairedWithADashboardThatExists(t *testing.T) {
	srv, _ := authFixture(t, "s3cret")
	c := client(t)
	_ = post(t, c, srv.URL+"/api/login", map[string]string{"name": "anna", "password": "s3cret"}).Body.Close()

	refused := []map[string]string{
		{"name": "hall TV"},
		{"name": "hall TV", "platform": "test"},
		{"name": "hall TV", "platform": "test", "dashboard": "nope"},
		{"name": "hall TV", "platform": "other", "dashboard": "ops"},
	}
	for _, body := range refused {
		res := post(t, c, srv.URL+"/api/display-tokens", body)
		_ = res.Body.Close()
		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("%v gave %d, want a screen to be refused without a real dashboard",
				body, res.StatusCode)
		}
	}

	// And the pairing reaches the screen, which is how it knows what to show.
	res := post(t, c, srv.URL+"/api/display-tokens", map[string]string{
		"name": "hall TV", "platform": "test", "dashboard": "ops",
	})
	defer func() { _ = res.Body.Close() }()
	var token struct{ Secret string }
	if err := json.NewDecoder(res.Body).Decode(&token); err != nil {
		t.Fatal(err)
	}
	session, err := http.Get(srv.URL + "/api/session?token=" + token.Secret)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Body.Close() }()
	var who map[string]any
	if err := json.NewDecoder(session.Body).Decode(&who); err != nil {
		t.Fatal(err)
	}
	if who["dashboard"] != "ops" || who["platform"] != "test" {
		t.Errorf("session = %+v, want the arrangement this screen is paired with", who)
	}
}

func TestRepeatedWrongPasswordsAreSlowedDown(t *testing.T) {
	srv, _ := authFixture(t, "s3cret")
	c := client(t)
	wrong := map[string]string{"name": "anna", "password": "nope"}
	for i := 0; i < 10; i++ {
		res := post(t, c, srv.URL+"/api/login", wrong)
		_ = res.Body.Close()
		if res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status = %d, want 401", i+1, res.StatusCode)
		}
	}
	res := post(t, c, srv.URL+"/api/login", map[string]string{"name": "anna", "password": "s3cret"})
	_ = res.Body.Close()
	if res.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429 while the limit is in force", res.StatusCode)
	}
	if res.Header.Get("Retry-After") == "" {
		t.Error("no Retry-After header")
	}
}

func TestADisplayCarriesItsOwnLookAndCanBeChangedLater(t *testing.T) {
	srv, _ := authFixture(t, "s3cret")
	c := client(t)
	_ = post(t, c, srv.URL+"/api/login", map[string]string{"name": "anna", "password": "s3cret"}).Body.Close()
	res := post(t, c, srv.URL+"/api/display-tokens", map[string]any{
		"name": "hall", "platform": "test", "dashboard": "ops",
		"options": map[string]any{"showSystem": true, "theme": "dark"},
	})
	var token struct {
		ID      string
		Secret  string
		Options struct {
			ShowSystem bool
			ShowLive   bool
			Theme      string
		}
	}
	if err := json.NewDecoder(res.Body).Decode(&token); err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if !token.Options.ShowSystem || token.Options.Theme != "dark" {
		t.Fatalf("options = %+v", token.Options)
	}

	session := func() map[string]any {
		t.Helper()
		res, err := http.Get(srv.URL + "/api/session?token=" + token.Secret)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		var body map[string]any
		if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		display, _ := body["display"].(map[string]any)
		return display
	}
	if d := session(); d["showSystem"] != true || d["theme"] != "dark" {
		t.Errorf("session display = %v", d)
	}

	edit := do(t, c, http.MethodPut, srv.URL+"/api/display-tokens/"+token.ID, map[string]any{"showLive": true, "theme": "neon"})
	_ = edit.Body.Close()
	if edit.StatusCode != http.StatusOK {
		t.Fatalf("edit gave %d", edit.StatusCode)
	}
	if d := session(); d["showLive"] != true || d["showSystem"] != nil || d["theme"] != nil {
		t.Errorf("after the edit, session display = %v; an unknown theme means follow the screen", d)
	}

	pick := do(t, c, http.MethodPut, srv.URL+"/api/display-tokens/"+token.ID, map[string]any{"theme": "screen"})
	_ = pick.Body.Close()
	if d := session(); d["theme"] != "screen" {
		t.Errorf("session display = %v, want the choice left to the screen", d)
	}

	server, err := http.Get(srv.URL + "/api/server?token=" + token.Secret)
	if err != nil {
		t.Fatal(err)
	}
	_ = server.Body.Close()
	if server.StatusCode == http.StatusForbidden {
		t.Error("a display may not read the system figures it is set to show")
	}
	put := do(t, http.DefaultClient, http.MethodPut, srv.URL+"/api/display-tokens/"+token.ID+"?token="+token.Secret, map[string]any{})
	_ = put.Body.Close()
	if put.StatusCode != http.StatusForbidden {
		t.Errorf("a display changed its own options: %d", put.StatusCode)
	}
}

func loginFrom(t *testing.T, h http.Handler, remote string, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = remote
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestOneClientsWrongPasswordsDoNotLockOutAnother(t *testing.T) {
	manager, err := auth.Open(auth.Options{Password: "s3cret"})
	if err != nil {
		t.Fatal(err)
	}
	h := api.Handler(api.Deps{
		Log:     slog.New(slog.NewTextHandler(discard{}, nil)),
		Metrics: prometheus.NewRegistry(),
		Auth:    manager,
	})
	for i := 0; i < 10; i++ {
		_ = loginFrom(t, h, "203.0.113.9:4000", `{"name":"x","password":"nope"}`)
	}
	if rec := loginFrom(t, h, "203.0.113.9:4001", `{"name":"x","password":"s3cret"}`); rec.Code != http.StatusTooManyRequests {
		t.Errorf("same address from another port gave %d, want 429", rec.Code)
	}
	if rec := loginFrom(t, h, "198.51.100.7:4000", `{"name":"anna","password":"s3cret"}`); rec.Code != http.StatusOK {
		t.Errorf("another address gave %d, want it to log in", rec.Code)
	}
}

func TestLoginRefusesAnOversizedBodyAndALongName(t *testing.T) {
	srv, _ := authFixture(t, "s3cret")
	c := client(t)
	huge := map[string]string{"name": "anna", "password": strings.Repeat("x", 8<<10)}
	res := post(t, c, srv.URL+"/api/login", huge)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("oversized body gave %d", res.StatusCode)
	}
	long := map[string]string{"name": strings.Repeat("a", 65), "password": "s3cret"}
	res = post(t, c, srv.URL+"/api/login", long)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("long name gave %d", res.StatusCode)
	}
	fine := map[string]string{"name": strings.Repeat("ą", 64), "password": "s3cret"}
	res = post(t, c, srv.URL+"/api/login", fine)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Errorf("64-character name gave %d", res.StatusCode)
	}
}

func TestCrossSiteWritesAreRefused(t *testing.T) {
	srv, _ := authFixture(t, "s3cret")
	c := client(t)
	_ = post(t, c, srv.URL+"/api/login", map[string]string{"name": "anna", "password": "s3cret"}).Body.Close()
	host := strings.TrimPrefix(srv.URL, "http://")

	cases := []struct {
		name        string
		path        string
		contentType string
		headers     map[string]string
		want        int
	}{
		{"plain text message", "/api/message", "text/plain", nil, http.StatusUnsupportedMediaType},
		{"form login", "/api/login", "application/x-www-form-urlencoded", nil, http.StatusUnsupportedMediaType},
		{"no content type", "/api/message", "", nil, http.StatusUnsupportedMediaType},
		{"foreign origin", "/api/message", "application/json", map[string]string{"Origin": "https://evil.example"}, http.StatusForbidden},
		{"foreign origin login", "/api/login", "application/json", map[string]string{"Origin": "https://evil.example"}, http.StatusForbidden},
		{"null origin", "/api/message", "application/json", map[string]string{"Origin": "null"}, http.StatusForbidden},
		{"cross-site fetch", "/api/message", "application/json", map[string]string{"Sec-Fetch-Site": "cross-site"}, http.StatusForbidden},
		{"same-site fetch", "/api/message", "application/json", map[string]string{"Sec-Fetch-Site": "same-site"}, http.StatusForbidden},
		{"own origin", "/api/message", "application/json; charset=utf-8", map[string]string{"Origin": "http://" + host, "Sec-Fetch-Site": "same-origin"}, http.StatusOK},
		{"no browser headers", "/api/message", "application/json", nil, http.StatusOK},
	}
	for _, tc := range cases {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+tc.path, strings.NewReader(`{"text":"hi","name":"anna","password":"s3cret"}`))
		if tc.contentType != "" {
			req.Header.Set("Content-Type", tc.contentType)
		}
		for k, v := range tc.headers {
			req.Header.Set(k, v)
		}
		res, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode != tc.want {
			t.Errorf("%s: status = %d, want %d", tc.name, res.StatusCode, tc.want)
		}
	}

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/message", strings.NewReader(`{"text":"`+strings.Repeat("x", 2<<20)+`"}`))
	req.Header.Set("Content-Type", "application/json")
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("a 2 MiB message gave %d, want it refused", res.StatusCode)
	}
}

func TestAConfiguredOriginMayWrite(t *testing.T) {
	manager, err := auth.Open(auth.Options{})
	if err != nil {
		t.Fatal(err)
	}
	h := api.Handler(api.Deps{
		Log:       slog.New(slog.NewTextHandler(discard{}, nil)),
		Metrics:   prometheus.NewRegistry(),
		Auth:      manager,
		WSOrigins: []string{"localhost:5173"},
	})
	for origin, want := range map[string]int{
		"http://localhost:5173": http.StatusOK,
		"http://localhost:6000": http.StatusForbidden,
	} {
		req := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"name":"anna"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", origin)
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Errorf("%s: status = %d, want %d", origin, rec.Code, want)
		}
	}
}

func TestChangingThePasswordLogsEveryoneOut(t *testing.T) {
	db, err := store.Open(store.Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	before, err := auth.Open(auth.Options{DB: db.DB(), Password: "s3cret"})
	if err != nil {
		t.Fatal(err)
	}
	value, _, err := before.Login("anna", "s3cret")
	if err != nil {
		t.Fatal(err)
	}
	after, err := auth.Open(auth.Options{DB: db.DB(), Password: "changed"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := after.Verify(value); ok {
		t.Fatal("a cookie from before the password change still worked")
	}
}

func TestTheDefaultPasswordIsFlaggedOnlyToThoseLoggedIn(t *testing.T) {
	sessionOf := func(c *http.Client, url string) map[string]any {
		t.Helper()
		res, err := c.Get(url + "/api/session")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		var session map[string]any
		if err := json.NewDecoder(res.Body).Decode(&session); err != nil {
			t.Fatal(err)
		}
		return session
	}

	srv, _ := authFixture(t, auth.DefaultPassword)
	c := client(t)
	if _, told := sessionOf(c, srv.URL)["defaultPassword"]; told {
		t.Fatal("a visitor who has not logged in was told the password is the default one")
	}
	res := post(t, c, srv.URL+"/api/login", map[string]string{"name": "anna", "password": auth.DefaultPassword})
	var login map[string]any
	if err := json.NewDecoder(res.Body).Decode(&login); err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if login["defaultPassword"] != true || sessionOf(c, srv.URL)["defaultPassword"] != true {
		t.Fatalf("login = %+v, want the default password flagged", login)
	}

	srv, _ = authFixture(t, "s3cret")
	c = client(t)
	_ = post(t, c, srv.URL+"/api/login", map[string]string{"name": "anna", "password": "s3cret"}).Body.Close()
	if sessionOf(c, srv.URL)["defaultPassword"] != false {
		t.Fatal("a changed password is still flagged as the default one")
	}
}
