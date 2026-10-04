package store

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// AlertWriter batches alert history the way the log writer batches log rows.
//
// It differs from that writer in what it does when it cannot keep up. Dropping
// one log row out of millions costs nothing; dropping an alert costs a gap in
// the record, and a full queue means a storm — the moment the record matters
// most. Blocking is still not an option, because the caller is the path that
// raises alerts and stalling it would back up the intake behind it. So this
// writer drops too, but it drops nothing silently: every loss is counted, and
// the count is reported with the other writer stats so a gap in the history is
// visible rather than invisible.
type AlertWriter struct {
	store *Store
	opts  WriterOptions

	in      chan []AlertRow
	done    chan struct{}
	stopped sync.Once
	// mu keeps a late Write from sending on the channel Stop closed.
	mu     sync.RWMutex
	closed bool

	written atomic.Int64
	dropped atomic.Int64
	batches atomic.Int64
	errors  atomic.Int64
	// held is set while bursts wait for a busy database.
	held bool
}

// NewAlertWriter starts the alert writer.
func NewAlertWriter(s *Store, o WriterOptions) *AlertWriter {
	if o.FlushEvery <= 0 {
		o.FlushEvery = time.Second
	}
	if o.Queue <= 0 {
		// Alerts are orders of magnitude rarer than logs, so a queue this deep
		// is never reached by ordinary noise — only by something pathological,
		// which is exactly what the drop counter is there to show.
		o.Queue = 1024
	}
	if o.Log == nil {
		o.Log = slog.Default()
	}
	w := &AlertWriter{
		store: s,
		opts:  o,
		in:    make(chan []AlertRow, o.Queue),
		done:  make(chan struct{}),
	}
	go w.run()
	return w
}

// Write queues one burst's current state. The live store holds the running
// count, so a row that is dropped here is repaired by the next occurrence of
// the same burst; only a burst that never repeats is lost outright.
func (w *AlertWriter) Write(rows ...AlertRow) {
	if len(rows) == 0 {
		return
	}
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.closed {
		w.dropped.Add(int64(len(rows)))
		return
	}
	select {
	case w.in <- rows:
	default:
		w.dropped.Add(int64(len(rows)))
		w.opts.Log.Warn("alert history queue is full", "dropped", len(rows))
	}
}

// Stop flushes what is queued and stops the writer.
func (w *AlertWriter) Stop() {
	w.stopped.Do(func() {
		w.mu.Lock()
		w.closed = true
		close(w.in)
		w.mu.Unlock()
		<-w.done
	})
}

// Stats summarises the writer, dropped alerts included.
func (w *AlertWriter) Stats() WriterStats {
	return WriterStats{
		Written: w.written.Load(),
		Dropped: w.dropped.Load(),
		Batches: w.batches.Load(),
		Errors:  w.errors.Load(),
		Queued:  len(w.in),
	}
}

func (w *AlertWriter) run() {
	defer close(w.done)
	t := time.NewTicker(w.opts.FlushEvery)
	defer t.Stop()

	var pending []AlertRow
	for {
		select {
		case batch, ok := <-w.in:
			if !ok {
				if left := w.flush(pending); len(left) > 0 {
					w.dropped.Add(int64(len(left)))
					w.opts.Log.Error("gave up alert history still held at shutdown", "rows", len(left))
				}
				return
			}
			pending = append(pending, batch...)
			if len(pending) >= 1000 && !w.held {
				pending = w.flush(pending)
			}
		case <-t.C:
			if len(pending) > 0 {
				pending = w.flush(pending)
			}
		}
	}
}

// flush writes the batch, keeping only the latest state of each burst: fifty
// occurrences in one second are one statement carrying the final count, not
// fifty.
func (w *AlertWriter) flush(rows []AlertRow) []AlertRow {
	if len(rows) == 0 {
		return nil
	}
	latest := make(map[string]AlertRow, len(rows))
	order := make([]string, 0, len(rows))
	for _, r := range rows {
		if _, seen := latest[r.ID]; !seen {
			order = append(order, r.ID)
		}
		latest[r.ID] = r
	}
	batch := make([]AlertRow, 0, len(order))
	for _, id := range order {
		batch = append(batch, latest[id])
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := w.store.WriteAlerts(ctx, batch); err != nil {
		if isBusy(err) && len(batch) < maxHeldAlerts {
			if !w.held {
				w.opts.Log.Warn("database busy; holding alert history to write again", "rows", len(batch))
			}
			w.held = true
			return batch
		}
		w.errors.Add(1)
		w.dropped.Add(int64(len(batch)))
		w.opts.Log.Error("could not write alert history", "rows", len(batch), "error", err)
		w.held = false
		return nil
	}
	if w.held {
		w.held = false
		w.opts.Log.Info("alert history written after the database was busy", "rows", len(batch))
	}
	w.batches.Add(1)
	w.written.Add(int64(len(batch)))
	return nil
}

// maxHeldAlerts bounds the alert bursts kept for a busy database.
const maxHeldAlerts = 100_000
