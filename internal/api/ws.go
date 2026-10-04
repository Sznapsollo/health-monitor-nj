package api

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/hub"
	"github.com/Sznapsollo/health-monitor-nj/internal/visits"
	"github.com/coder/websocket"
)

const (
	// writeTimeout bounds how long one message may take to reach a viewer.
	writeTimeout = 10 * time.Second
	// idleTimeout closes a socket that has said nothing at all; the browser
	// pings every 30 s.
	idleTimeout = 5 * time.Minute
	// readLimit caps what a viewer may send: criteria are small.
	readLimit = 64 << 10
	sendQueue = 64
)

var (
	errViewerBehind = errors.New("viewer is not keeping up")
	errViewerGone   = errors.New("viewer has gone")
)

// wsSender only queues: the hub calls Send from the UDP reader's goroutine,
// so a stalled socket must never be waited on. A full queue is an error.
type wsSender struct {
	conn  *websocket.Conn
	ctx   context.Context
	stop  context.CancelFunc
	queue chan []byte
	done  chan struct{}
}

func newWSSender(ctx context.Context, stop context.CancelFunc, conn *websocket.Conn) *wsSender {
	s := &wsSender{
		conn:  conn,
		ctx:   ctx,
		stop:  stop,
		queue: make(chan []byte, sendQueue),
		done:  make(chan struct{}),
	}
	go s.run()
	return s
}

func (s *wsSender) Send(msg hub.ServerMessage) error {
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return s.enqueue(b)
}

func (s *wsSender) SendRaw(msg []byte) error { return s.enqueue(msg) }

func (s *wsSender) Close() { s.stop() }

func (s *wsSender) enqueue(b []byte) error {
	select {
	case <-s.done:
		return errViewerGone
	default:
	}
	select {
	case s.queue <- b:
		return nil
	case <-s.done:
		return errViewerGone
	default:
		return errViewerBehind
	}
}

func (s *wsSender) run() {
	defer close(s.done)
	for {
		select {
		case <-s.ctx.Done():
			return
		case b := <-s.queue:
			ctx, cancel := context.WithTimeout(s.ctx, writeTimeout)
			err := s.conn.Write(ctx, websocket.MessageText, b)
			cancel()
			if err != nil {
				s.stop()
				return
			}
		}
	}
}

// handleWS upgrades a request and runs one viewer's session.
func (d Deps) handleWS(w http.ResponseWriter, r *http.Request) {
	if d.Hub == nil {
		http.Error(w, "live updates are not available", http.StatusServiceUnavailable)
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// The SPA is served from this same origin in production; the Vite dev
		// server proxies to it, so the browser's origin matches either way.
		OriginPatterns: d.WSOrigins,
	})
	if err != nil {
		d.Log.Debug("websocket upgrade failed", "error", err)
		return
	}
	conn.SetReadLimit(readLimit)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	sender := newWSSender(ctx, cancel, conn)
	session := d.Hub.Add(sender)
	defer d.Hub.Remove(session.ID)
	client := clientOf(r)
	session.SetClient(client)
	visitID := ""
	if d.Visits != nil {
		defer func() {
			if visitID != "" {
				info := session.Info()
				d.Visits.Disconnect(context.WithoutCancel(ctx), visitID, info.Sent, info.Signals)
			}
		}()
	}

	d.Log.Debug("viewer connected", "session", session.ID, "remote", r.RemoteAddr)
	defer d.Log.Debug("viewer gone", "session", session.ID)

	for {
		readCtx, readCancel := context.WithTimeout(ctx, idleTimeout)
		typ, data, err := conn.Read(readCtx)
		readCancel()
		if err != nil {
			closeStatus := websocket.CloseStatus(err)
			switch {
			case closeStatus == websocket.StatusNormalClosure, closeStatus == websocket.StatusGoingAway:
			case errors.Is(err, context.Canceled):
			default:
				d.Log.Debug("websocket read ended", "session", session.ID, "error", err)
			}
			_ = conn.Close(websocket.StatusNormalClosure, "")
			return
		}
		if typ != websocket.MessageText {
			continue
		}

		var msg hub.ClientMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			_ = sender.Send(hub.ServerMessage{T: hub.TypeError, Text: "could not read that message"})
			continue
		}
		session.Touch(time.Now())

		switch msg.T {
		case hub.TypeHello:
			err = d.Hub.Hello(session, msg)
			if d.Visits != nil && visitID == "" {
				visitID = d.Visits.Connect(ctx, visits.Who{
					Tab: msg.Tab, Login: client.Login, Kind: client.Kind, IP: client.IP,
					UserAgent: client.UserAgent, Platform: session.Info().Platform,
				})
			}
		case hub.TypeCriteria:
			err = d.Hub.Criteria(session, msg)
		case hub.TypePing:
			err = sender.Send(hub.ServerMessage{T: hub.TypePong, ServerTime: time.Now()})
		default:
			err = sender.Send(hub.ServerMessage{T: hub.TypeError, Text: "unknown message type " + msg.T})
		}
		if err != nil {
			_ = conn.Close(websocket.StatusInternalError, "send failed")
			return
		}
	}
}

// handleSessions lists the connected viewers, which is the old "Sesje HM".
func (d Deps) handleSessions(w http.ResponseWriter, r *http.Request) {
	if d.Hub == nil {
		writeJSON(w, http.StatusOK, map[string]any{"sessions": []any{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": d.Hub.Sessions()})
}

// clientOf says who opened a connection. The address is the proxy's when the
// monitor sits behind one; X-Forwarded-For is taken as the sender's word.
func clientOf(r *http.Request) hub.Client {
	c := hub.Client{UserAgent: r.UserAgent()}
	if session, ok := SessionOf(r); ok {
		c.Login = strings.TrimPrefix(session.Name, "display: ")
		c.Kind = string(session.Kind)
	}
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		c.IP = strings.TrimSpace(strings.Split(forwarded, ",")[0])
	} else if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		c.IP = host
	} else {
		c.IP = r.RemoteAddr
	}
	return c
}

// handleVisits is the viewing history of one UTC day (?day=YYYYMMDD), today
// when none is given.
func (d Deps) handleVisits(w http.ResponseWriter, r *http.Request) {
	if d.Visits == nil {
		writeJSON(w, http.StatusOK, map[string]any{"visits": []any{}})
		return
	}
	day := r.URL.Query().Get("day")
	if day == "" {
		day = time.Now().UTC().Format("20060102")
	}
	list, err := d.Visits.Day(r.Context(), day)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"day": day, "visits": list})
}

func (d Deps) handleVisitDays(w http.ResponseWriter, r *http.Request) {
	days := []string{time.Now().UTC().Format("20060102")}
	if d.Visits != nil {
		stored, err := d.Visits.Days(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		for _, day := range stored {
			if day != days[0] {
				days = append(days, day)
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"days": days})
}
