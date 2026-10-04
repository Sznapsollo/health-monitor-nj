package api_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/api"
	"github.com/Sznapsollo/health-monitor-nj/internal/hub"
	"github.com/Sznapsollo/health-monitor-nj/internal/signal"
	"github.com/Sznapsollo/health-monitor-nj/internal/state"
	hmstore "github.com/Sznapsollo/health-monitor-nj/internal/store"
	"github.com/Sznapsollo/health-monitor-nj/internal/visits"
	"github.com/coder/websocket"
	"github.com/prometheus/client_golang/prometheus"
)

var now = time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)

type wsFixture struct {
	server *httptest.Server
	hub    *hub.Hub
	store  *state.Store
}

func newWSFixture(t *testing.T) *wsFixture {
	t.Helper()
	reg := signal.NewRegistry()
	err := reg.Add(&signal.Definition{
		Platform: "test", Name: "requests", Kind: signal.KindTimeseries,
		Dims:   []string{"url", "user"},
		Values: signal.Values{Count: "count", MS: "ms"},
	})
	if err != nil {
		t.Fatal(err)
	}
	store := state.NewStore(reg, func() time.Time { return now })
	viewers := hub.New(store, reg, func() time.Time { return now }, time.Millisecond)
	db, err := hmstore.Open(hmstore.Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	history, err := visits.Open(context.Background(), db.DB(), time.Now)
	if err != nil {
		t.Fatal(err)
	}

	h := api.Handler(api.Deps{
		Log:      slog.New(slog.NewTextHandler(discard{}, nil)),
		Metrics:  prometheus.NewRegistry(),
		Registry: reg,
		Store:    store,
		Hub:      viewers,
		Visits:   history,
		Started:  now,
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &wsFixture{server: srv, hub: viewers, store: store}
}

func (f *wsFixture) dial(t *testing.T) (*websocket.Conn, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	url := "ws" + strings.TrimPrefix(f.server.URL, "http") + "/ws"
	conn, res, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if res != nil && res.Body != nil {
		_ = res.Body.Close()
	}
	t.Cleanup(func() { _ = conn.Close(websocket.StatusNormalClosure, "") })
	return conn, ctx
}

func send(t *testing.T, conn *websocket.Conn, ctx context.Context, msg hub.ClientMessage) {
	t.Helper()
	b, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Write(ctx, websocket.MessageText, b); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func read(t *testing.T, conn *websocket.Conn, ctx context.Context) hub.ServerMessage {
	t.Helper()
	readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, data, err := conn.Read(readCtx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var msg hub.ServerMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return msg
}

func (f *wsFixture) feed(url string, ms float64) {
	var a state.Agg
	a.Add(1, ms, true)
	f.store.Apply([]state.Sample{{
		Platform: "test", Signal: "requests", Minute: state.MinuteOf(now),
		Dims: map[string]string{"url": url}, Agg: a,
	}})
}

func TestWebSocketHelloAndLiveUpdate(t *testing.T) {
	f := newWSFixture(t)
	f.feed("/a", 100)

	conn, ctx := f.dial(t)
	send(t, conn, ctx, hub.ClientMessage{
		T: hub.TypeHello, Name: "anna",
		Criteria: []state.Criteria{{Signal: "requests", HistoryMinutes: 10, Group: "url"}},
	})

	snapshot := read(t, conn, ctx)
	if snapshot.T != hub.TypeSnapshot {
		t.Fatalf("first message = %q, want a snapshot", snapshot.T)
	}
	view := snapshot.Signals["requests"]
	if view == nil || len(view.Groups) != 1 || view.Groups[0].Value != "/a" {
		t.Fatalf("snapshot = %+v", snapshot.Signals)
	}

	// A new packet reaches the viewer as an event on the next flush.
	f.hub.Dirty(dirtyOf(f, "/b", 200))
	f.hub.Flush()

	event := read(t, conn, ctx)
	if event.T != hub.TypeEvent || event.Signal != "requests" {
		t.Fatalf("event = %+v", event)
	}
	if event.View == nil || len(event.View.Total) != 1 {
		t.Fatalf("view = %+v", event.View)
	}
	if event.View.Total[0].Count != 2 {
		t.Errorf("count = %d, want both packets of the minute", event.View.Total[0].Count)
	}
}

func TestWebSocketBatchedUpdate(t *testing.T) {
	f := newWSFixture(t)
	f.feed("/a", 100)

	conn, ctx := f.dial(t)
	send(t, conn, ctx, hub.ClientMessage{
		T: hub.TypeHello, Batch: true,
		Criteria: []state.Criteria{
			{ID: "all", Signal: "requests", HistoryMinutes: 10},
			{ID: "byUrl", Signal: "requests", HistoryMinutes: 10, Group: "url"},
		},
	})
	if snapshot := read(t, conn, ctx); snapshot.T != hub.TypeSnapshot {
		t.Fatalf("first message = %q, want a snapshot", snapshot.T)
	}

	f.hub.Dirty(dirtyOf(f, "/b", 200))
	f.hub.Flush()

	msg := read(t, conn, ctx)
	if msg.T != hub.TypeEvents || len(msg.Events) != 2 {
		t.Fatalf("message = %+v, want one events message with both views", msg)
	}
	for _, e := range msg.Events {
		if e.Signal != "requests" || e.View == nil || e.View.Total[0].Count != 2 {
			t.Errorf("event %s = %+v", e.ID, e)
		}
	}
}

func dirtyOf(f *wsFixture, url string, ms float64) []state.Dirty {
	var a state.Agg
	a.Add(1, ms, true)
	dirty, _ := f.store.Apply([]state.Sample{{
		Platform: "test", Signal: "requests", Minute: state.MinuteOf(now),
		Dims: map[string]string{"url": url}, Agg: a,
	}})
	return dirty
}

func TestWebSocketCriteriaChange(t *testing.T) {
	f := newWSFixture(t)
	f.feed("/a", 100)
	conn, ctx := f.dial(t)

	send(t, conn, ctx, hub.ClientMessage{
		T: hub.TypeHello, Criteria: []state.Criteria{{Signal: "requests", HistoryMinutes: 10}},
	})
	if got := read(t, conn, ctx).Signals["requests"]; len(got.Groups) != 0 {
		t.Fatalf("groups = %+v, want none yet", got.Groups)
	}

	send(t, conn, ctx, hub.ClientMessage{
		T: hub.TypeCriteria, Criteria: []state.Criteria{{Signal: "requests", HistoryMinutes: 10, Group: "url"}},
	})
	msg := read(t, conn, ctx)
	if msg.T != hub.TypeSnapshot || len(msg.Signals["requests"].Groups) != 1 {
		t.Fatalf("message = %+v", msg)
	}
}

func TestWebSocketPingPong(t *testing.T) {
	f := newWSFixture(t)
	conn, ctx := f.dial(t)
	send(t, conn, ctx, hub.ClientMessage{T: hub.TypePing})
	if got := read(t, conn, ctx); got.T != hub.TypePong {
		t.Fatalf("message = %+v, want a pong", got)
	}
}

func TestWebSocketRejectsNonsense(t *testing.T) {
	f := newWSFixture(t)
	conn, ctx := f.dial(t)

	if err := conn.Write(ctx, websocket.MessageText, []byte("{not json")); err != nil {
		t.Fatal(err)
	}
	if got := read(t, conn, ctx); got.T != hub.TypeError {
		t.Fatalf("message = %+v, want an error", got)
	}

	send(t, conn, ctx, hub.ClientMessage{T: "dance"})
	if got := read(t, conn, ctx); got.T != hub.TypeError {
		t.Fatalf("message = %+v, want an error for an unknown type", got)
	}
}

func TestSessionsEndpointListsViewers(t *testing.T) {
	f := newWSFixture(t)
	conn, ctx := f.dial(t)
	send(t, conn, ctx, hub.ClientMessage{
		T: hub.TypeHello, Name: "anna",
		Criteria: []state.Criteria{{Signal: "requests"}},
	})
	read(t, conn, ctx)

	res, err := f.server.Client().Get(f.server.URL + "/api/sessions")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var body struct {
		Sessions []hub.SessionInfo `json:"sessions"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Sessions) != 1 || body.Sessions[0].Name != "anna" {
		t.Fatalf("sessions = %+v", body.Sessions)
	}
}

func TestDisconnectRemovesTheSession(t *testing.T) {
	f := newWSFixture(t)
	conn, ctx := f.dial(t)
	send(t, conn, ctx, hub.ClientMessage{T: hub.TypeHello, Criteria: []state.Criteria{{Signal: "requests"}}})
	read(t, conn, ctx)
	if len(f.hub.Sessions()) != 1 {
		t.Fatal("the session was not registered")
	}

	_ = conn.Close(websocket.StatusNormalClosure, "")
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(f.hub.Sessions()) == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the session outlived the connection")
}

func TestAViewerIsListedWithItsAddressAndBrowser(t *testing.T) {
	f := newWSFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(f.server.URL, "http") + "/ws"
	conn, res, err := websocket.Dial(ctx, url, &websocket.DialOptions{
		HTTPHeader: http.Header{"User-Agent": []string{"Mozilla/5.0 (X11; Linux x86_64) Chrome/128.0 Safari/537.36"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res != nil && res.Body != nil {
		_ = res.Body.Close()
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()
	send(t, conn, ctx, hub.ClientMessage{T: hub.TypeHello, Criteria: []state.Criteria{{Signal: "requests"}}})
	read(t, conn, ctx)

	list, err := f.server.Client().Get(f.server.URL + "/api/sessions")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = list.Body.Close() }()
	var body struct {
		Sessions []hub.SessionInfo `json:"sessions"`
	}
	if err := json.NewDecoder(list.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Sessions) != 1 || body.Sessions[0].IP != "127.0.0.1" || !strings.Contains(body.Sessions[0].UserAgent, "Chrome/128") {
		t.Errorf("sessions = %+v", body.Sessions)
	}
}

func TestATabIsOneVisitInTheViewingHistory(t *testing.T) {
	f := newWSFixture(t)
	for range 2 {
		conn, ctx := f.dial(t)
		send(t, conn, ctx, hub.ClientMessage{T: hub.TypeHello, Tab: "tab-7", Criteria: []state.Criteria{{Signal: "requests"}}})
		read(t, conn, ctx)
		_ = conn.Close(websocket.StatusNormalClosure, "")
		time.Sleep(50 * time.Millisecond)
	}

	res, err := f.server.Client().Get(f.server.URL + "/api/visits")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	var body struct{ Visits []visits.Visit }
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Visits) != 1 || body.Visits[0].Reconnects != 1 || body.Visits[0].IP != "127.0.0.1" ||
		len(body.Visits[0].Signals) != 1 || body.Visits[0].Open {
		t.Errorf("visits = %+v, want one closed visit with one reconnect", body.Visits)
	}
}
