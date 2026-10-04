// Package hub keeps the connected viewers, decides what each of them should
// see, and paces the updates. It knows nothing about WebSockets: a session is
// anything that can be handed a message.
package hub

import (
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/alert"
	"github.com/Sznapsollo/health-monitor-nj/internal/state"
)

// Message types on the wire.
const (
	// client → server
	TypeHello    = "hello"
	TypeCriteria = "criteria"
	TypePing     = "ping"

	// server → client
	TypeSnapshot = "snapshot"
	TypeEvent    = "event"
	TypeEvents   = "events"
	TypeAlert    = "alert"
	TypeNotice   = "notice"
	TypePong     = "pong"
	TypeError    = "error"
)

// ClientMessage is anything a browser sends.
type ClientMessage struct {
	T string `json:"t"`
	// Name is the viewer's own label, shown in the sessions list.
	Name string `json:"name,omitempty"`
	// Platform the viewer is looking at; empty means the only one there is.
	Platform string `json:"platform,omitempty"`
	// Criteria is one entry per signal the viewer wants.
	Criteria []state.Criteria `json:"criteria,omitempty"`
	// Batch says the viewer understands TypeEvents; pages loaded before it
	// existed keep getting one TypeEvent per chart and minute.
	Batch bool `json:"batch,omitempty"`
	// Tab identifies the browser tab across reconnects and reloads, so its
	// visit is one row in the viewing history.
	Tab string `json:"tab,omitempty"`
}

// Event is one changed minute of one criteria entry.
type Event struct {
	ID     string      `json:"id"`
	Signal string      `json:"signal"`
	Minute int64       `json:"minute"`
	View   *state.View `json:"view"`
}

// ServerMessage is anything the server sends.
type ServerMessage struct {
	T          string    `json:"t"`
	ServerTime time.Time `json:"serverTime,omitempty"`

	// Snapshot: one view per criteria entry, keyed by its id.
	Platforms []string               `json:"platforms,omitempty"`
	Signals   map[string]*state.View `json:"signals,omitempty"`

	// Event: one criteria entry, usually one changed minute.
	ID     string      `json:"id,omitempty"`
	Signal string      `json:"signal,omitempty"`
	Minute int64       `json:"minute,omitempty"`
	View   *state.View `json:"view,omitempty"`

	// Events: everything that changed since the last flush, in one message.
	Events []Event `json:"events,omitempty"`

	// Alert: one alert as it is raised or folded into.
	Alert *alert.Alert `json:"alert,omitempty"`
	// New says the alert is not a repeat, so it may deserve a sound.
	New bool `json:"new,omitempty"`

	// Notice and error.
	Level string `json:"level,omitempty"`
	Text  string `json:"text,omitempty"`
	// Popup asks the browser to interrupt the viewer; the wall display
	// ignores it.
	Popup bool `json:"popup,omitempty"`
}
