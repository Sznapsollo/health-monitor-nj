package intake

import (
	"net/netip"
	"sync"
	"sync/atomic"
	"time"
)

const (
	TapSize   = 500
	TapRaw    = 4 << 10
	TapWindow = 30 * time.Second
)

// TapEntry is one datagram as it arrived, with what the pipeline made of it.
type TapEntry struct {
	Seq      int64     `json:"seq"`
	At       time.Time `json:"at"`
	From     string    `json:"from"`
	Port     int       `json:"port"`
	Platform string    `json:"platform,omitempty"`
	Sender   string    `json:"sender,omitempty"`
	Type     string    `json:"type,omitempty"`
	Rejected string    `json:"rejected,omitempty"`
	Size     int       `json:"size"`
	Raw      string    `json:"raw"`
	Cut      bool      `json:"cut,omitempty"`
}

// Tap keeps the last TapSize datagrams in memory, and only while someone
// watches: Watch switches it on for TapWindow, and a tap nobody renews
// costs one atomic load per datagram.
type Tap struct {
	until atomic.Int64

	mu    sync.Mutex
	ring  []TapEntry
	kept  int64
	seq   int64
	clear *time.Timer
}

func (t *Tap) on(now time.Time) bool {
	return t != nil && now.UnixNano() < t.until.Load()
}

func (t *Tap) watched() bool {
	return t != nil && t.on(time.Now())
}

// Watch keeps the tap on for another TapWindow; after a pause it starts
// empty, so what it shows was received while someone looked.
func (t *Tap) Watch(now time.Time) {
	t.mu.Lock()
	if !t.on(now) {
		t.ring, t.kept = nil, 0
	}
	t.until.Store(now.Add(TapWindow).UnixNano())
	if t.clear == nil {
		t.clear = time.AfterFunc(TapWindow+time.Second, t.free)
	} else {
		t.clear.Reset(TapWindow + time.Second)
	}
	t.mu.Unlock()
}

func (t *Tap) free() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.on(time.Now()) {
		t.ring, t.kept = nil, 0
	}
}

// Since returns the kept datagrams after seq, oldest first, and the last seq;
// a seq from before a restart reads them all.
func (t *Tap) Since(seq int64) ([]TapEntry, int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := []TapEntry{}
	if seq > t.seq {
		seq = 0
	}
	for i := max(t.seq-t.kept, seq) + 1; i <= t.seq; i++ {
		out = append(out, t.ring[i%TapSize])
	}
	return out, t.seq
}

func (t *Tap) record(now time.Time, raw []byte, from netip.Addr, port int, platform, sender, kind, rejected string) {
	if !t.on(now) {
		return
	}
	cut := len(raw) > TapRaw
	e := TapEntry{
		At: now, Port: port, Platform: platform, Sender: sender,
		Type: kind, Rejected: rejected, Size: len(raw), Raw: string(raw[:min(len(raw), TapRaw)]), Cut: cut,
	}
	if from.IsValid() {
		e.From = from.Unmap().String()
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.ring == nil {
		t.ring = make([]TapEntry, TapSize)
	}
	t.seq++
	t.kept = min(t.kept+1, TapSize)
	e.Seq = t.seq
	t.ring[t.seq%TapSize] = e
}
