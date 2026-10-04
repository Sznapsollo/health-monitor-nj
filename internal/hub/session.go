package hub

import (
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/state"
)

// Session is one connected viewer: who they are, what they asked for, and
// which minutes they have not been told about yet.
type Session struct {
	ID string

	mu        sync.Mutex
	name      string
	platform  string
	criteria  []state.Criteria
	pending   map[state.Dirty]struct{}
	send_     Sender
	connected time.Time
	lastSeen  time.Time
	sent      int64
	deferrals int
	batch     bool
	// generation changes with the criteria, so updates taken under old
	// criteria are not put back after a failed send.
	generation int

	client Client
}

// Client is who opened a session: read once when it connects, never per update.
type Client struct {
	// Login is the name the viewer logged in with, or the wall display's name.
	Login string `json:"login,omitempty"`
	// Kind is "user" or "display"; empty when no login is required.
	Kind      string `json:"kind,omitempty"`
	IP        string `json:"ip,omitempty"`
	UserAgent string `json:"userAgent,omitempty"`
}

// SetClient records who is on the other end.
func (s *Session) SetClient(c Client) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.client = c
}

// SessionInfo is what the sessions list shows.
type SessionInfo struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Platform  string    `json:"platform"`
	Signals   []string  `json:"signals"`
	Connected time.Time `json:"connected"`
	LastSeen  time.Time `json:"lastSeen"`
	Sent      int64     `json:"sent"`
	Client
}

func newSession(seq int64, send Sender, now time.Time) *Session {
	return &Session{
		ID:        "s" + strconv.FormatInt(seq, 10),
		send_:     send,
		pending:   make(map[state.Dirty]struct{}),
		connected: now,
		lastSeen:  now,
	}
}

func (s *Session) setIdentity(name, requested, resolved string, batch bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.batch = s.batch || batch
	if name != "" {
		s.name = name
	}
	if resolved != "" {
		s.platform = resolved
	}
}

func (s *Session) setCriteria(criteria []state.Criteria) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.criteria = s.criteria[:0]
	byID := make(map[string]int, len(criteria))
	for _, c := range criteria {
		if c.Signal == "" {
			continue
		}
		c = c.Normalise()
		if i, ok := byID[c.ID]; ok {
			s.criteria[i] = c
			continue
		}
		byID[c.ID] = len(s.criteria)
		s.criteria = append(s.criteria, c)
	}
	// Criteria that changed make the pending updates stale: the next flush
	// would trim them the old way. The snapshot that follows covers them.
	clear(s.pending)
	s.generation++
}

// snapshotCriteria returns the platform and a copy of the criteria.
func (s *Session) snapshotCriteria() (string, []state.Criteria) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]state.Criteria, len(s.criteria))
	copy(out, s.criteria)
	return s.platform, out
}

func (s *Session) batches() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.batch
}

// Name is the viewer's label.
func (s *Session) Name() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.name
}

// Info summarises the session for the sessions list.
func (s *Session) Info() SessionInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	signals := make([]string, 0, len(s.criteria))
	seen := make(map[string]bool, len(s.criteria))
	for _, c := range s.criteria {
		if !seen[c.Signal] {
			seen[c.Signal] = true
			signals = append(signals, c.Signal)
		}
	}
	return SessionInfo{
		ID: s.ID, Name: s.name, Platform: s.platform, Signals: signals,
		Connected: s.connected, LastSeen: s.lastSeen, Sent: s.sent,
		Client: s.client,
	}
}

// mark notes that these minutes changed. Several changes to the same minute
// between two flushes collapse into one update, which is the coalescing the
// old server did per (session, topic, key).
func (s *Session) mark(dirty []state.Dirty) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.criteria) == 0 {
		return
	}
	for _, d := range dirty {
		if d.Platform != s.platform {
			continue
		}
		for _, c := range s.criteria {
			if c.Signal == d.Signal {
				s.pending[d] = struct{}{}
				break
			}
		}
	}
}

// takePending returns what has changed and clears it, oldest minute first,
// with the generation of the criteria it was taken under.
func (s *Session) takePending() ([]state.Dirty, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.pending) == 0 {
		return nil, s.generation
	}
	out := make([]state.Dirty, 0, len(s.pending))
	for d := range s.pending {
		out = append(out, d)
	}
	clear(s.pending)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Signal != out[j].Signal {
			return out[i].Signal < out[j].Signal
		}
		return out[i].Minute < out[j].Minute
	})
	return out, s.generation
}

// restorePending puts back updates a failed send did not deliver, unless the
// criteria changed meanwhile and the snapshot sent then already covers them.
func (s *Session) restorePending(dirty []state.Dirty, generation int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if generation != s.generation {
		return
	}
	for _, d := range dirty {
		s.pending[d] = struct{}{}
	}
}

// Touch records that the viewer is still there.
func (s *Session) Touch(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastSeen = now
}

func (s *Session) send(msg ServerMessage) error {
	return s.deliver(func(sender Sender) error { return sender.Send(msg) })
}

func (s *Session) sendRaw(msg []byte) error {
	return s.deliver(func(sender Sender) error { return sender.SendRaw(msg) })
}

func (s *Session) deliver(send func(Sender) error) error {
	s.mu.Lock()
	sender := s.send_
	s.mu.Unlock()
	if sender == nil {
		return nil
	}
	if err := send(sender); err != nil {
		s.mu.Lock()
		s.deferrals++
		s.mu.Unlock()
		return err
	}
	s.mu.Lock()
	s.sent++
	s.mu.Unlock()
	return nil
}

func (s *Session) close() {
	s.mu.Lock()
	sender := s.send_
	s.mu.Unlock()
	if sender != nil {
		sender.Close()
	}
}

func (s *Session) deferred() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deferrals
}

func (s *Session) resetDeferrals() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deferrals = 0
}

func sortSessions(in []SessionInfo) {
	// Two viewers can connect in the same millisecond, and the sessions come
	// out of a map, so without a tie-break the list reorders itself between
	// reads for no reason anyone can see.
	sort.Slice(in, func(i, j int) bool {
		if !in[i].Connected.Equal(in[j].Connected) {
			return in[i].Connected.Before(in[j].Connected)
		}
		return in[i].ID < in[j].ID
	})
}
