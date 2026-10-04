package hub

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/alert"
	"github.com/Sznapsollo/health-monitor-nj/internal/state"
)

// Defaults taken from the old server's event-bus settings, which set the pace
// the charts have always updated at.
const (
	DefaultBroadcastDelay = 4 * time.Second
	// MaxDeferrals is how often a session may be skipped because it is not
	// keeping up before it is dropped. The old server used the same number to
	// force a broadcast through.
	MaxDeferrals = 30
)

// Sender is how the hub reaches one viewer. A WebSocket connection is the
// only implementation in production; tests use a channel.
type Sender interface {
	// Send delivers one message. It must not block for long; an error counts
	// towards dropping the session.
	Send(msg ServerMessage) error
	// SendRaw delivers a message already encoded, which is how one encoding
	// reaches every viewer that wants the same thing.
	SendRaw(msg []byte) error
	// Close ends the connection, so a dropped viewer reconnects rather than
	// sitting on a socket that is no longer fed.
	Close()
}

// PlatformLister names the platforms that exist. The registry knows them from
// the catalogues, so a dashboard opened before any packet arrives still has a
// platform to look at; the hot state only learns a platform once it is fed.
type PlatformLister interface {
	Platforms() []string
}

// Hub owns the sessions and paces what they receive.
type Hub struct {
	store     *state.Store
	platforms PlatformLister
	now       func() time.Time
	delay     time.Duration

	mu       sync.RWMutex
	sessions map[string]*Session
	seq      int64
}

// New builds a hub over the hot state. lister may be nil, in which case only
// platforms that have already received data are offered.
func New(store *state.Store, lister PlatformLister, now func() time.Time, delay time.Duration) *Hub {
	if now == nil {
		now = time.Now
	}
	if delay <= 0 {
		delay = DefaultBroadcastDelay
	}
	if lister == nil {
		lister = store
	}
	return &Hub{
		store:     store,
		platforms: lister,
		now:       now,
		delay:     delay,
		sessions:  make(map[string]*Session),
	}
}

// knownPlatforms is everything the viewer may switch between.
func (h *Hub) knownPlatforms() []string {
	seen := map[string]bool{}
	var out []string
	for _, list := range [][]string{h.platforms.Platforms(), h.store.Platforms()} {
		for _, p := range list {
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	sort.Strings(out)
	return out
}

// Delay is how long changes are collected before a session is updated.
func (h *Hub) Delay() time.Duration { return h.delay }

// Add registers a viewer and returns its session.
func (h *Hub) Add(send Sender) *Session {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.seq++
	s := newSession(h.seq, send, h.now())
	h.sessions[s.ID] = s
	return s
}

// Remove drops a viewer.
func (h *Hub) Remove(id string) {
	h.mu.Lock()
	delete(h.sessions, id)
	h.mu.Unlock()
	// A viewer that has gone is no reason to keep breaking its charts down.
	h.applyPairs()
}

// Sessions lists the connected viewers, oldest first.
func (h *Hub) Sessions() []SessionInfo {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]SessionInfo, 0, len(h.sessions))
	for _, s := range h.sessions {
		out = append(out, s.Info())
	}
	sortSessions(out)
	return out
}

// Hello applies a viewer's opening message and answers with a snapshot.
func (h *Hub) Hello(s *Session, msg ClientMessage) error {
	s.setIdentity(msg.Name, msg.Platform, h.defaultPlatform(msg.Platform), msg.Batch)
	s.setCriteria(msg.Criteria)
	h.applyPairs()
	return s.send(h.snapshot(s))
}

// Criteria applies a change of criteria and re-sends the affected snapshot.
func (h *Hub) Criteria(s *Session, msg ClientMessage) error {
	if msg.Platform != "" {
		s.setIdentity(s.Name(), msg.Platform, msg.Platform, msg.Batch)
	}
	s.setCriteria(msg.Criteria)
	h.applyPairs()
	return s.send(h.snapshot(s))
}

func (h *Hub) defaultPlatform(requested string) string {
	if requested != "" {
		return requested
	}
	if ps := h.knownPlatforms(); len(ps) > 0 {
		return ps[0]
	}
	return ""
}

// applyPairs tells the store which dimension pairs are being looked at, so
// only those are precomputed. A signal nobody asks for any more
// is cleared as well: a viewer hiding a chart has to stop the work behind it,
// not merely stop it being sent.
func (h *Hub) applyPairs() {
	wanted := map[string]map[state.Pair]bool{}
	h.mu.RLock()
	for _, other := range h.sessions {
		platform, criteria := other.snapshotCriteria()
		for _, c := range criteria {
			pair, ok := c.Pair()
			if !ok {
				continue
			}
			key := platform + "\x00" + c.Signal
			if wanted[key] == nil {
				wanted[key] = map[state.Pair]bool{}
			}
			wanted[key][pair] = true
		}
	}
	h.mu.RUnlock()

	for key, pairs := range wanted {
		platform, sig := splitKey(key)
		list := make([]state.Pair, 0, len(pairs))
		for p := range pairs {
			list = append(list, p)
		}
		h.store.SetActivePairs(platform, sig, list)
	}

	// Whatever is still being precomputed but is now on nobody's list.
	for _, platform := range h.store.Platforms() {
		for _, sig := range h.store.ActivePairSignals(platform) {
			if _, ok := wanted[platform+"\x00"+sig]; !ok {
				h.store.SetActivePairs(platform, sig, nil)
			}
		}
	}
}

// snapshot builds the whole picture for one session.
func (h *Hub) snapshot(s *Session) ServerMessage {
	platform, criteria := s.snapshotCriteria()
	msg := ServerMessage{
		T:          TypeSnapshot,
		ServerTime: h.now(),
		Platforms:  h.knownPlatforms(),
		Signals:    make(map[string]*state.View, len(criteria)),
	}
	for _, c := range criteria {
		view, ok := h.store.View(platform, c.Signal, c)
		if !ok {
			continue
		}
		v := view
		msg.Signals[c.ID] = &v
	}
	return msg
}

// Dirty receives the minutes that changed. It only marks work; the sending
// happens on the session's own cadence, so a busy minute does not turn into a
// message per packet.
func (h *Hub) Dirty(dirty []state.Dirty) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, s := range h.sessions {
		s.mark(dirty)
	}
}

// Flush sends every session what has changed since the last flush. The caller
// runs it on a ticker at Delay().
func (h *Hub) Flush() {
	h.mu.RLock()
	sessions := make([]*Session, 0, len(h.sessions))
	for _, s := range h.sessions {
		sessions = append(sessions, s)
	}
	h.mu.RUnlock()

	views := viewCache{}
	for _, s := range sessions {
		if dropped := h.flushOne(s, views); dropped {
			h.Remove(s.ID)
			s.close()
		}
	}
}

// viewCache holds the views built during one flush, encoded, so viewers
// with the same chart settings share one build and one encoding.
type viewCache map[string][]byte

func (vc viewCache) get(store *state.Store, platform string, c state.Criteria, minutes []int64) []byte {
	key := viewKey(platform, c, minutes)
	if raw, ok := vc[key]; ok {
		return raw
	}
	var raw []byte
	if view, ok := store.MinutesView(platform, c.Signal, c, minutes); ok {
		raw, _ = json.Marshal(view)
	}
	vc[key] = raw
	return raw
}

// viewKey identifies a view by everything that shapes it; the viewer's own
// ID for the chart does not.
func viewKey(platform string, c state.Criteria, minutes []int64) string {
	c.ID = ""
	spec, _ := json.Marshal(c)
	var b strings.Builder
	b.WriteString(platform)
	b.WriteByte(0)
	b.Write(spec)
	for _, m := range minutes {
		b.WriteByte(0)
		b.WriteString(strconv.FormatInt(m, 10))
	}
	return b.String()
}

// wireEvent is an Event whose view is already encoded.
type wireEvent struct {
	id     string
	signal string
	minute int64
	view   []byte
}

// flushOne sends one session's pending updates and reports whether it should
// be dropped for not keeping up. Whatever a failed send did not deliver is
// kept for the next flush. Each chart gets one event carrying every minute of
// it that changed.
func (h *Hub) flushOne(s *Session, views viewCache) bool {
	platform, criteria := s.snapshotCriteria()
	dirty, generation := s.takePending()

	minutes := make(map[string][]int64)
	for _, d := range dirty {
		if d.Platform == platform {
			minutes[d.Signal] = append(minutes[d.Signal], d.Minute)
		}
	}
	var events []wireEvent
	for _, c := range criteria {
		keys := minutes[c.Signal]
		if len(keys) == 0 {
			continue
		}
		raw := views.get(h.store, platform, c, keys)
		if raw == nil {
			continue
		}
		events = append(events, wireEvent{id: c.ID, signal: c.Signal, minute: keys[len(keys)-1], view: raw})
	}
	if len(events) == 0 {
		s.resetDeferrals()
		return false
	}

	if err := h.sendEvents(s, events); err != nil {
		s.restorePending(dirty, generation)
		return s.deferred() >= MaxDeferrals
	}
	s.resetDeferrals()
	return false
}

func (h *Hub) sendEvents(s *Session, events []wireEvent) error {
	now, _ := h.now().MarshalJSON()
	if s.batches() {
		size := 64
		for _, e := range events {
			size += len(e.view) + len(e.id) + len(e.signal) + 64
		}
		buf := make([]byte, 0, size)
		buf = append(buf, `{"t":"events","serverTime":`...)
		buf = append(buf, now...)
		buf = append(buf, `,"events":[`...)
		for i, e := range events {
			if i > 0 {
				buf = append(buf, ',')
			}
			buf = append(buf, '{')
			buf = appendEventFields(buf, e)
			buf = append(buf, '}')
		}
		buf = append(buf, "]}"...)
		return s.sendRaw(buf)
	}
	for _, e := range events {
		buf := make([]byte, 0, len(e.view)+len(e.id)+len(e.signal)+128)
		buf = append(buf, `{"t":"event","serverTime":`...)
		buf = append(buf, now...)
		buf = append(buf, ',')
		buf = appendEventFields(buf, e)
		buf = append(buf, '}')
		if err := s.sendRaw(buf); err != nil {
			return err
		}
	}
	return nil
}

func appendEventFields(buf []byte, e wireEvent) []byte {
	id, _ := json.Marshal(e.id)
	signal, _ := json.Marshal(e.signal)
	buf = append(buf, `"id":`...)
	buf = append(buf, id...)
	buf = append(buf, `,"signal":`...)
	buf = append(buf, signal...)
	buf = append(buf, `,"minute":`...)
	buf = strconv.AppendInt(buf, e.minute, 10)
	buf = append(buf, `,"view":`...)
	return append(buf, e.view...)
}

// Alert pushes one alert to every viewer of its platform. Alerts are not
// coalesced the way chart minutes are: a viewer wants to know now.
func (h *Hub) Alert(a alert.Alert, isNew bool) {
	raw, err := json.Marshal(ServerMessage{T: TypeAlert, ServerTime: h.now(), Alert: &a, New: isNew})
	if err != nil {
		return
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, s := range h.sessions {
		platform, _ := s.snapshotCriteria()
		if platform != "" && a.Platform != "" && platform != a.Platform {
			continue
		}
		_ = s.sendRaw(raw)
	}
}

// Notice sends a message to every viewer, which is the old "send message to
// users" action.
func (h *Hub) Notice(level, text string, popup bool) {
	raw, err := json.Marshal(ServerMessage{T: TypeNotice, ServerTime: h.now(), Level: level, Text: text, Popup: popup})
	if err != nil {
		return
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, s := range h.sessions {
		_ = s.sendRaw(raw)
	}
}

// Run flushes on the broadcast cadence until stop is closed.
func (h *Hub) Run(stop <-chan struct{}) {
	t := time.NewTicker(h.delay)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			h.Flush()
		}
	}
}

func splitKey(key string) (string, string) {
	for i := 0; i < len(key); i++ {
		if key[i] == 0 {
			return key[:i], key[i+1:]
		}
	}
	return key, ""
}
