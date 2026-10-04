package store

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// LogWriter batches log rows the way the aggregate writer batches minutes. It
// never blocks the readers: when the queue fills, rows are sampled with a
// counter rather than waited on.
type LogWriter struct {
	store *Store
	opts  WriterOptions

	in      chan []LogRow
	done    chan struct{}
	stopped sync.Once
	// mu keeps a late Write from sending on the channel Stop closed.
	mu     sync.RWMutex
	closed bool

	written atomic.Int64
	sampled atomic.Int64
	errors  atomic.Int64
	// held is set while rows wait for a busy database; they are retried on
	// each tick rather than on every batch.
	held bool
	// sampleEvery is how many rows to keep one of once the queue is full.
	sampleEvery int64
	seen        atomic.Int64
}

// NewLogWriter starts the log writer.
func NewLogWriter(s *Store, o WriterOptions) *LogWriter {
	if o.FlushEvery <= 0 {
		o.FlushEvery = time.Second
	}
	if o.Queue <= 0 {
		o.Queue = 256
	}
	if o.Log == nil {
		o.Log = slog.Default()
	}
	w := &LogWriter{
		store:       s,
		opts:        o,
		in:          make(chan []LogRow, o.Queue),
		done:        make(chan struct{}),
		sampleEvery: 10,
	}
	go w.run()
	return w
}

// Write queues rows. A full queue means the database cannot keep up: rather
// than stall the intake, one row in ten is kept and the rest are counted.
func (w *LogWriter) Write(rows []LogRow) {
	if len(rows) == 0 {
		return
	}
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.closed {
		w.sampled.Add(int64(len(rows)))
		return
	}
	select {
	case w.in <- rows:
	default:
		kept := rows[:0]
		for _, r := range rows {
			if w.seen.Add(1)%w.sampleEvery == 0 {
				kept = append(kept, r)
			} else {
				w.sampled.Add(1)
			}
		}
		if len(kept) == 0 {
			return
		}
		select {
		case w.in <- kept:
		default:
			w.sampled.Add(int64(len(kept)))
		}
	}
}

// Stop flushes what is queued and stops the writer.
func (w *LogWriter) Stop() {
	w.stopped.Do(func() {
		w.mu.Lock()
		w.closed = true
		close(w.in)
		w.mu.Unlock()
		<-w.done
	})
}

// LogWriterStats reports what the log writer has done.
type LogWriterStats struct {
	Written int64 `json:"written"`
	Sampled int64 `json:"sampled"`
	Errors  int64 `json:"errors"`
	Queued  int   `json:"queued"`
}

// Stats summarises the writer.
func (w *LogWriter) Stats() LogWriterStats {
	return LogWriterStats{
		Written: w.written.Load(),
		Sampled: w.sampled.Load(),
		Errors:  w.errors.Load(),
		Queued:  len(w.in),
	}
}

func (w *LogWriter) run() {
	defer close(w.done)
	t := time.NewTicker(w.opts.FlushEvery)
	defer t.Stop()

	var pending []LogRow
	for {
		select {
		case batch, ok := <-w.in:
			if !ok {
				if left := w.flush(pending); len(left) > 0 {
					w.sampled.Add(int64(len(left)))
					w.opts.Log.Error("gave up log rows still held at shutdown", "rows", len(left))
				}
				return
			}
			pending = append(pending, batch...)
			if len(pending) >= 2000 && !w.held {
				pending = w.flush(pending)
			}
		case <-t.C:
			if len(pending) > 0 {
				pending = w.flush(pending)
			}
		}
	}
}

// maxHeldLogRows bounds what waits in memory for a busy database; past it
// the oldest rows are given up and counted.
const maxHeldLogRows = 500_000

// flush writes rows and returns what is to be tried again: the rows not
// written when the database was only busy, nothing otherwise.
func (w *LogWriter) flush(rows []LogRow) []LogRow {
	if len(rows) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	left, dropped, err := w.store.writeLogsLeft(ctx, rows)
	w.written.Add(int64(len(rows) - len(left) - dropped))
	if dropped > 0 {
		w.errors.Add(1)
		w.opts.Log.Error("could not write logs", "rows", dropped, "error", err)
	}
	if len(left) == 0 {
		if w.held {
			w.held = false
			w.opts.Log.Info("log rows written after the database was busy", "rows", len(rows))
		}
		return nil
	}
	if over := len(left) - maxHeldLogRows; over > 0 {
		w.errors.Add(1)
		w.sampled.Add(int64(over))
		w.opts.Log.Error("gave up log rows held for a busy database", "rows", over)
		left = left[over:]
	}
	if !w.held {
		w.held = true
		w.opts.Log.Warn("database busy; holding log rows to write again", "rows", len(left))
	}
	return left
}
