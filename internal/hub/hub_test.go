package hub_test

import (
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/hub"
	"github.com/Sznapsollo/health-monitor-nj/internal/signal"
	"github.com/Sznapsollo/health-monitor-nj/internal/state"
)

var base = time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)

// recorder is a viewer that just keeps what it was sent.
type recorder struct {
	mu     sync.Mutex
	msgs   []hub.ServerMessage
	err    error
	closed bool
}

func (r *recorder) Send(msg hub.ServerMessage) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	r.msgs = append(r.msgs, msg)
	return nil
}

func (r *recorder) SendRaw(raw []byte) error {
	var msg hub.ServerMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		return err
	}
	return r.Send(msg)
}

func (r *recorder) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
}

func (r *recorder) all() []hub.ServerMessage {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]hub.ServerMessage, len(r.msgs))
	copy(out, r.msgs)
	return out
}

func (r *recorder) last() hub.ServerMessage {
	all := r.all()
	if len(all) == 0 {
		return hub.ServerMessage{}
	}
	return all[len(all)-1]
}

func testHub(t *testing.T) (*hub.Hub, *state.Store) {
	t.Helper()
	reg := signal.NewRegistry()
	for _, name := range []string{"requests", "jobs"} {
		err := reg.Add(&signal.Definition{
			Platform: "test", Name: name, Kind: signal.KindTimeseries,
			Dims:   []string{"url", "user"},
			Values: signal.Values{Count: "count", MS: "ms"},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	store := state.NewStore(reg, func() time.Time { return base })
	return hub.New(store, reg, func() time.Time { return base }, time.Millisecond), store
}

func feed(store *state.Store, sig string, dims map[string]string, ms float64) []state.Dirty {
	var a state.Agg
	a.Add(1, ms, true)
	dirty, _ := store.Apply([]state.Sample{{
		Platform: "test", Signal: sig, Minute: state.MinuteOf(base), Dims: dims, Agg: a,
	}})
	return dirty
}

func TestHelloAnswersWithASnapshot(t *testing.T) {
	h, store := testHub(t)
	feed(store, "requests", map[string]string{"url": "/a"}, 100)

	rec := &recorder{}
	s := h.Add(rec)
	err := h.Hello(s, hub.ClientMessage{
		Name:     "anna",
		Criteria: []state.Criteria{{Signal: "requests", HistoryMinutes: 10, Group: "url"}},
	})
	if err != nil {
		t.Fatalf("hello: %v", err)
	}

	msg := rec.last()
	if msg.T != hub.TypeSnapshot {
		t.Fatalf("type = %q, want a snapshot", msg.T)
	}
	view := msg.Signals["requests"]
	if view == nil {
		t.Fatalf("signals = %+v, want requests", msg.Signals)
	}
	if len(view.Total) != 1 || view.Total[0].Count != 1 {
		t.Errorf("total = %+v", view.Total)
	}
	if len(view.Groups) != 1 || view.Groups[0].Value != "/a" {
		t.Errorf("groups = %+v", view.Groups)
	}
	if len(msg.Platforms) != 1 || msg.Platforms[0] != "test" {
		t.Errorf("platforms = %v", msg.Platforms)
	}
}

func TestOnlySubscribedSignalsAreSent(t *testing.T) {
	h, store := testHub(t)
	rec := &recorder{}
	s := h.Add(rec)
	// The viewer watches requests only.
	if err := h.Hello(s, hub.ClientMessage{
		Criteria: []state.Criteria{{Signal: "requests", HistoryMinutes: 10}},
	}); err != nil {
		t.Fatal(err)
	}

	h.Dirty(feed(store, "jobs", map[string]string{"url": "/j"}, 10))
	h.Flush()
	if got := len(rec.all()); got != 1 {
		t.Fatalf("messages = %d, want only the snapshot", got)
	}

	h.Dirty(feed(store, "requests", map[string]string{"url": "/a"}, 10))
	h.Flush()
	msgs := rec.all()
	if len(msgs) != 2 {
		t.Fatalf("messages = %d, want the snapshot and one event", len(msgs))
	}
	if msgs[1].T != hub.TypeEvent || msgs[1].Signal != "requests" {
		t.Errorf("event = %+v", msgs[1])
	}
	if msgs[1].View == nil || len(msgs[1].View.Total) != 1 {
		t.Errorf("view = %+v, want the one changed minute", msgs[1].View)
	}
}

func TestChangesCoalesceUntilTheFlush(t *testing.T) {
	h, store := testHub(t)
	rec := &recorder{}
	s := h.Add(rec)
	if err := h.Hello(s, hub.ClientMessage{
		Criteria: []state.Criteria{{Signal: "requests", HistoryMinutes: 10}},
	}); err != nil {
		t.Fatal(err)
	}

	// Fifty packets in the same minute are one update, not fifty.
	for i := 0; i < 50; i++ {
		h.Dirty(feed(store, "requests", map[string]string{"url": "/a"}, 10))
	}
	h.Flush()

	msgs := rec.all()
	if len(msgs) != 2 {
		t.Fatalf("messages = %d, want the snapshot and a single coalesced event", len(msgs))
	}
	if msgs[1].View.Total[0].Count != 50 {
		t.Errorf("count = %d, want every packet counted in the one update", msgs[1].View.Total[0].Count)
	}

	// Nothing new means nothing sent.
	h.Flush()
	if len(rec.all()) != 2 {
		t.Errorf("messages = %d, want no empty update", len(rec.all()))
	}
}

func TestCriteriaChangeResendsTheSnapshot(t *testing.T) {
	h, store := testHub(t)
	feed(store, "requests", map[string]string{"url": "/a", "user": "anna"}, 10)

	rec := &recorder{}
	s := h.Add(rec)
	if err := h.Hello(s, hub.ClientMessage{
		Criteria: []state.Criteria{{Signal: "requests", HistoryMinutes: 10}},
	}); err != nil {
		t.Fatal(err)
	}
	if got := rec.last().Signals["requests"]; len(got.Groups) != 0 {
		t.Fatalf("groups = %+v, want none before a group is asked for", got.Groups)
	}

	err := h.Criteria(s, hub.ClientMessage{
		Criteria: []state.Criteria{{Signal: "requests", HistoryMinutes: 10, Group: "url"}},
	})
	if err != nil {
		t.Fatalf("criteria: %v", err)
	}
	msg := rec.last()
	if msg.T != hub.TypeSnapshot {
		t.Fatalf("type = %q, want a fresh snapshot", msg.T)
	}
	if len(msg.Signals["requests"].Groups) != 1 {
		t.Errorf("groups = %+v, want the new grouping", msg.Signals["requests"].Groups)
	}
}

func TestAskingForASubGroupStartsKeepingThePair(t *testing.T) {
	h, store := testHub(t)
	rec := &recorder{}
	s := h.Add(rec)

	err := h.Hello(s, hub.ClientMessage{
		Criteria: []state.Criteria{{Signal: "requests", HistoryMinutes: 10, Group: "url", Sub: "user"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Packets arriving after the criteria are broken down by the pair.
	h.Dirty(feed(store, "requests", map[string]string{"url": "/a", "user": "anna"}, 10))
	h.Flush()

	view := rec.last().View
	if view == nil || len(view.Groups) != 1 {
		t.Fatalf("view = %+v", view)
	}
	if len(view.Groups[0].Groups) != 1 || view.Groups[0].Groups[0].Value != "anna" {
		t.Fatalf("sub-groups = %+v, want anna", view.Groups[0].Groups)
	}
}

func TestSessionsAreListedAndRemoved(t *testing.T) {
	h, _ := testHub(t)
	a := h.Add(&recorder{})
	b := h.Add(&recorder{})
	if err := h.Hello(a, hub.ClientMessage{Name: "anna", Criteria: []state.Criteria{{Signal: "requests"}}}); err != nil {
		t.Fatal(err)
	}
	if err := h.Hello(b, hub.ClientMessage{Name: "bob"}); err != nil {
		t.Fatal(err)
	}

	list := h.Sessions()
	if len(list) != 2 {
		t.Fatalf("sessions = %+v", list)
	}
	if list[0].Name != "anna" || list[0].Platform != "test" {
		t.Errorf("first session = %+v", list[0])
	}
	if len(list[0].Signals) != 1 || list[0].Signals[0] != "requests" {
		t.Errorf("signals = %v", list[0].Signals)
	}

	h.Remove(a.ID)
	if len(h.Sessions()) != 1 {
		t.Errorf("sessions = %+v, want one left", h.Sessions())
	}
}

func TestASessionThatCannotKeepUpIsDropped(t *testing.T) {
	h, store := testHub(t)
	rec := &recorder{}
	s := h.Add(rec)
	if err := h.Hello(s, hub.ClientMessage{
		Criteria: []state.Criteria{{Signal: "requests", HistoryMinutes: 10}},
	}); err != nil {
		t.Fatal(err)
	}

	rec.mu.Lock()
	rec.err = errors.New("connection is backed up")
	rec.mu.Unlock()

	for i := 0; i <= hub.MaxDeferrals; i++ {
		h.Dirty(feed(store, "requests", map[string]string{"url": "/a"}, 10))
		h.Flush()
	}
	if len(h.Sessions()) != 0 {
		t.Fatalf("sessions = %+v, want the stuck one dropped", h.Sessions())
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if !rec.closed {
		t.Error("a dropped viewer's connection was left open, so it would never reconnect")
	}
}

func TestAFailedSendIsRetriedOnTheNextFlush(t *testing.T) {
	h, store := testHub(t)
	rec := &recorder{}
	s := h.Add(rec)
	if err := h.Hello(s, hub.ClientMessage{
		Batch: true, Criteria: []state.Criteria{{Signal: "requests", HistoryMinutes: 10}},
	}); err != nil {
		t.Fatal(err)
	}

	rec.mu.Lock()
	rec.err = errors.New("queue full")
	rec.mu.Unlock()
	h.Dirty(feed(store, "requests", map[string]string{"url": "/a"}, 10))
	h.Flush()

	rec.mu.Lock()
	rec.err = nil
	rec.mu.Unlock()
	h.Flush()

	msg := rec.last()
	if msg.T != hub.TypeEvents || len(msg.Events) != 1 || msg.Events[0].View.Total[0].Count != 1 {
		t.Fatalf("after recovery got %+v, want the minute that failed to send", msg)
	}
	if len(h.Sessions()) != 1 {
		t.Error("one failed flush should not drop the viewer")
	}
}

func TestUpdatesFromOldCriteriaAreNotRetried(t *testing.T) {
	h, store := testHub(t)
	rec := &recorder{}
	s := h.Add(rec)
	if err := h.Hello(s, hub.ClientMessage{
		Batch: true, Criteria: []state.Criteria{{Signal: "requests", HistoryMinutes: 10}},
	}); err != nil {
		t.Fatal(err)
	}

	rec.mu.Lock()
	rec.err = errors.New("queue full")
	rec.mu.Unlock()
	h.Dirty(feed(store, "requests", map[string]string{"url": "/a"}, 10))
	h.Flush()

	rec.mu.Lock()
	rec.err = nil
	rec.mu.Unlock()
	if err := h.Criteria(s, hub.ClientMessage{
		Criteria: []state.Criteria{{Signal: "jobs", HistoryMinutes: 10}},
	}); err != nil {
		t.Fatal(err)
	}
	before := len(rec.all())
	h.Flush()
	if got := len(rec.all()); got != before {
		t.Errorf("messages = %d, want nothing: the snapshot already covered the new criteria", got-before)
	}
}

func TestNoticeReachesEveryone(t *testing.T) {
	h, _ := testHub(t)
	a, b := &recorder{}, &recorder{}
	h.Add(a)
	h.Add(b)
	h.Notice("warn", "restarting in a minute", true)

	for name, rec := range map[string]*recorder{"a": a, "b": b} {
		msg := rec.last()
		if msg.T != hub.TypeNotice || msg.Text != "restarting in a minute" || !msg.Popup {
			t.Errorf("%s got %+v", name, msg)
		}
	}
}

func TestConcurrentUse(t *testing.T) {
	h, store := testHub(t)
	for i := 0; i < 4; i++ {
		s := h.Add(&recorder{})
		if err := h.Hello(s, hub.ClientMessage{
			Criteria: []state.Criteria{{Signal: "requests", HistoryMinutes: 10, Group: "url"}},
		}); err != nil {
			t.Fatal(err)
		}
	}

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				h.Dirty(feed(store, "requests", map[string]string{"url": "/a"}, 10))
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 200; j++ {
			h.Flush()
			h.Sessions()
		}
	}()
	wg.Wait()
}

func TestHidingAChartStopsTheWorkBehindIt(t *testing.T) {
	h, store := testHub(t)
	rec := &recorder{}
	s := h.Add(rec)

	err := h.Hello(s, hub.ClientMessage{Criteria: []state.Criteria{
		{Signal: "requests", HistoryMinutes: 10, Group: "url", Sub: "user"},
		{Signal: "jobs", HistoryMinutes: 10, Group: "url", Sub: "user"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got := store.ActivePairSignals("test"); len(got) != 2 {
		t.Fatalf("precomputed = %v, want both signals", got)
	}

	// The viewer unticks one chart: it leaves the criteria altogether.
	err = h.Criteria(s, hub.ClientMessage{Criteria: []state.Criteria{
		{Signal: "requests", HistoryMinutes: 10, Group: "url", Sub: "user"},
	}})
	if err != nil {
		t.Fatalf("criteria: %v", err)
	}
	if got := store.ActivePairSignals("test"); len(got) != 1 || got[0] != "requests" {
		t.Errorf("precomputed = %v, want only the chart still on show", got)
	}

	// Nothing is sent for it either, however busy it gets.
	before := len(rec.all())
	h.Dirty(feed(store, "jobs", map[string]string{"url": "/a", "user": "anna"}, 10))
	h.Flush()
	if got := len(rec.all()); got != before {
		t.Errorf("messages = %d, want no update for a hidden chart", got)
	}

	// And the last viewer leaving releases the rest.
	h.Remove(s.ID)
	if got := store.ActivePairSignals("test"); len(got) != 0 {
		t.Errorf("precomputed = %v, want nothing kept for a viewer that has gone", got)
	}
}

func TestOneSignalTwoWays(t *testing.T) {
	h, store := testHub(t)
	feed(store, "requests", map[string]string{"url": "/a", "user": "anna"}, 100)
	feed(store, "jobs", map[string]string{"url": "/j"}, 5)
	rec := &recorder{}
	s := h.Add(rec)
	err := h.Hello(s, hub.ClientMessage{Criteria: []state.Criteria{
		{ID: "byUrl", Signal: "requests", HistoryMinutes: 10, Group: "url"},
		{ID: "byUser", Signal: "requests", HistoryMinutes: 10, Group: "user"},
		{Signal: "jobs", HistoryMinutes: 10},
	}})
	if err != nil {
		t.Fatal(err)
	}
	snap := rec.last()
	if len(snap.Signals) != 3 {
		t.Fatalf("snapshot keys = %v, want one per criteria id", keysOf(snap.Signals))
	}
	if snap.Signals["byUrl"].Groups[0].Value != "/a" || snap.Signals["byUser"].Groups[0].Value != "anna" {
		t.Fatalf("byUrl = %+v, byUser = %+v", snap.Signals["byUrl"].Groups, snap.Signals["byUser"].Groups)
	}
	if _, ok := snap.Signals["jobs"]; !ok {
		t.Fatal("an entry without an id is keyed by its signal")
	}

	h.Dirty(feed(store, "requests", map[string]string{"url": "/b", "user": "bob"}, 10))
	h.Flush()
	events := rec.all()[1:]
	if len(events) != 2 || events[0].ID != "byUrl" || events[1].ID != "byUser" {
		t.Fatalf("events = %+v, want one per view of the changed signal", events)
	}
	if info := s.Info(); len(info.Signals) != 2 {
		t.Errorf("session signals = %v, want each signal once", info.Signals)
	}
}

func TestABatchingViewerGetsOneMessagePerFlush(t *testing.T) {
	h, store := testHub(t)
	rec := &recorder{}
	s := h.Add(rec)
	err := h.Hello(s, hub.ClientMessage{Batch: true, Criteria: []state.Criteria{
		{ID: "byUrl", Signal: "requests", HistoryMinutes: 10, Group: "url"},
		{ID: "byUser", Signal: "requests", HistoryMinutes: 10, Group: "user"},
		{Signal: "jobs", HistoryMinutes: 10},
	}})
	if err != nil {
		t.Fatal(err)
	}

	h.Dirty(feed(store, "requests", map[string]string{"url": "/b", "user": "bob"}, 10))
	h.Dirty(feed(store, "jobs", map[string]string{"url": "/j"}, 5))
	h.Flush()

	msgs := rec.all()[1:]
	if len(msgs) != 1 || msgs[0].T != hub.TypeEvents {
		t.Fatalf("messages = %+v, want a single events message", msgs)
	}
	var ids []string
	for _, e := range msgs[0].Events {
		ids = append(ids, e.ID)
	}
	sort.Strings(ids)
	if len(ids) != 3 || ids[0] != "byUrl" || ids[1] != "byUser" || ids[2] != "jobs" {
		t.Errorf("events = %v, want every changed view in the one message", ids)
	}
}

func keysOf(m map[string]*state.View) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestViewersWithTheSameChartEachGetTheirOwnID(t *testing.T) {
	h, store := testHub(t)
	a, b := &recorder{}, &recorder{}
	for id, rec := range map[string]*recorder{"mine": a, "theirs": b} {
		s := h.Add(rec)
		if err := h.Hello(s, hub.ClientMessage{Batch: true, Criteria: []state.Criteria{
			{ID: id, Signal: "requests", HistoryMinutes: 10, Group: "url"},
		}}); err != nil {
			t.Fatal(err)
		}
	}

	h.Dirty(feed(store, "requests", map[string]string{"url": "/a"}, 10))
	h.Flush()

	for id, rec := range map[string]*recorder{"mine": a, "theirs": b} {
		msg := rec.last()
		if msg.T != hub.TypeEvents || len(msg.Events) != 1 || msg.Events[0].ID != id {
			t.Fatalf("%s got %+v", id, msg)
		}
		if g := msg.Events[0].View.Groups; len(g) != 1 || g[0].Value != "/a" {
			t.Errorf("%s groups = %+v", id, g)
		}
	}
}

func TestSeveralChangedMinutesArriveInOneEvent(t *testing.T) {
	h, store := testHub(t)
	rec := &recorder{}
	s := h.Add(rec)
	if err := h.Hello(s, hub.ClientMessage{Batch: true, Criteria: []state.Criteria{
		{Signal: "requests", HistoryMinutes: 10},
	}}); err != nil {
		t.Fatal(err)
	}

	var a state.Agg
	a.Add(1, 5, true)
	dirty, _ := store.Apply([]state.Sample{
		{Platform: "test", Signal: "requests", Minute: state.MinuteOf(base) - 1, Dims: map[string]string{"url": "/a"}, Agg: a},
		{Platform: "test", Signal: "requests", Minute: state.MinuteOf(base), Dims: map[string]string{"url": "/a"}, Agg: a},
	})
	h.Dirty(dirty)
	h.Flush()

	msg := rec.last()
	if msg.T != hub.TypeEvents || len(msg.Events) != 1 {
		t.Fatalf("got %+v, want one event for the one chart", msg)
	}
	if e := msg.Events[0]; len(e.View.Total) != 2 || e.Minute != state.MinuteOf(base) {
		t.Errorf("event = minute %d with %d points, want both minutes, stamped with the latest", e.Minute, len(e.View.Total))
	}
}
