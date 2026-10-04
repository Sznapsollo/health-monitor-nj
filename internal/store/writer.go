package store

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Sznapsollo/health-monitor-nj/internal/state"
)

// accumulate adds into an existing row rather than replacing it, because the
// minute still in progress is written more than once. Durations only widen when the
// incoming row carries measurements.
const accumulate = `
    count   = count + excluded.count,
    sum_ms  = sum_ms + excluded.sum_ms,
    samples = samples + excluded.samples,
    min_ms  = CASE WHEN excluded.samples = 0 THEN min_ms
                   WHEN samples = 0 THEN excluded.min_ms
                   ELSE MIN(min_ms, excluded.min_ms) END,
    max_ms  = CASE WHEN excluded.samples = 0 THEN max_ms
                   WHEN samples = 0 THEN excluded.max_ms
                   ELSE MAX(max_ms, excluded.max_ms) END`

const (
	upsertTotal = `INSERT INTO agg_total (platform, signal, minute, count, sum_ms, min_ms, max_ms, samples)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (platform, signal, minute) DO UPDATE SET` + accumulate
	upsertDim = `INSERT INTO agg_dim (platform, signal, minute, dim, value, count, sum_ms, min_ms, max_ms, samples)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (platform, signal, minute, dim, value) DO UPDATE SET` + accumulate
	upsertPair = `INSERT INTO agg_pair (platform, signal, minute, dim, value, sub, sub_value, count, sum_ms, min_ms, max_ms, samples)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (platform, signal, minute, dim, sub, value, sub_value) DO UPDATE SET` + accumulate
	upsertLabel = `INSERT INTO dim_label (platform, signal, dim, value, name, seen)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (platform, signal, dim, value) DO UPDATE SET name = excluded.name, seen = excluded.seen
WHERE excluded.seen >= dim_label.seen`
)

// labelRefresh is how often, in minutes, an unchanged name has its seen
// minute written again.
const labelRefresh = 60

// Layout is what the writer stores of one signal: the value of each declared
// dimension, the pairs being kept, and at most MaxKeys values per dimension
// per minute (the rest in state.OtherBucket), as memory does.
type Layout struct {
	Dims    []string
	Pairs   []state.Pair
	MaxKeys int
	// Labels is dimension -> the dimension naming its values.
	Labels map[string]string
}

// WriterOptions configure the batched writer.
type WriterOptions struct {
	// FlushEvery bounds how long a sample of the minute in progress waits
	// before it is durable; a minute is written as soon as it closes.
	FlushEvery time.Duration
	// Queue is how many flushes may wait. When it fills, samples are dropped
	// with a counter rather than blocking the readers.
	Queue int
	Log   *slog.Logger
	// Layout says how to store a signal; false skips it. Without one, every
	// dimension a sample carries is stored, uncapped and with no pairs.
	Layout func(platform, signal string) (Layout, bool)
}

// Writer folds samples per dimension and writes one transaction at a time.
type Writer struct {
	store *Store
	opts  WriterOptions

	in      chan []state.Sample
	done    chan struct{}
	stopped sync.Once
	// mu keeps a late Write from sending on the channel Stop closed.
	mu     sync.RWMutex
	closed bool

	// seen holds the values stored per dimension per minute, so the cap is
	// applied across flushes of the same minute. Only the writer goroutine
	// touches it.
	seen map[capKey]map[string]struct{}
	// named is the last name written per labelled value, and namedPerDim how
	// many values each dimension has named; only the writer goroutine
	// touches them.
	named       map[labelKey]namedAt
	namedPerDim map[labelDim]int

	written atomic.Int64
	dropped atomic.Int64
	batches atomic.Int64
	errors  atomic.Int64
}

type labelDim struct {
	platform, signal, dim string
}

type labelKey struct {
	labelDim
	value string
}

type namedAt struct {
	name   string
	minute int64
}

type capKey struct {
	platform, signal, dim string
	minute                int64
}

// NewWriter starts the writer goroutine.
func NewWriter(s *Store, o WriterOptions) *Writer {
	if o.FlushEvery <= 0 {
		o.FlushEvery = DefaultFlushEvery
	}
	if o.Queue <= 0 {
		o.Queue = 256
	}
	if o.Log == nil {
		o.Log = slog.Default()
	}
	w := &Writer{
		store: s,
		opts:  o,
		in:    make(chan []state.Sample, o.Queue),
		done:  make(chan struct{}),
		seen:  map[capKey]map[string]struct{}{},

		named:       map[labelKey]namedAt{},
		namedPerDim: map[labelDim]int{},
	}
	go w.run()
	return w
}

// Samples queues one flush. It never blocks: a full queue means the database
// cannot keep up, which is counted and reported, not waited on.
func (w *Writer) Samples(samples []state.Sample) {
	if len(samples) == 0 {
		return
	}
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.closed {
		w.dropped.Add(int64(len(samples)))
		return
	}
	select {
	case w.in <- samples:
	default:
		w.dropped.Add(int64(len(samples)))
	}
}

// Stop flushes what is queued and stops the writer.
func (w *Writer) Stop() {
	w.stopped.Do(func() {
		w.mu.Lock()
		w.closed = true
		close(w.in)
		w.mu.Unlock()
		<-w.done
	})
}

// WriterStats reports what the writer has done.
type WriterStats struct {
	Written int64 `json:"written"`
	Dropped int64 `json:"dropped"`
	Batches int64 `json:"batches"`
	Errors  int64 `json:"errors"`
	Queued  int   `json:"queued"`
}

// Stats summarises the writer.
func (w *Writer) Stats() WriterStats {
	return WriterStats{
		Written: w.written.Load(),
		Dropped: w.dropped.Load(),
		Batches: w.batches.Load(),
		Errors:  w.errors.Load(),
		Queued:  len(w.in),
	}
}

// DefaultFlushEvery is how often the minute in progress is written.
const DefaultFlushEvery = 15 * time.Second

// maxPendingRows writes early rather than hold more than this many rows.
const maxPendingRows = 100_000

// run folds every batch into running rows as it arrives, so a row is written
// once per flush however many samples touched it: when its minute closes, and
// every FlushEvery for the minute still in progress.
func (w *Writer) run() {
	defer close(w.done)
	t := time.NewTicker(w.opts.FlushEvery)
	defer t.Stop()

	pending := newFolded()
	var samples int64
	var latest int64
	held := false
	flush := func() {
		if samples == 0 {
			return
		}
		switch err := w.write(pending, samples); {
		case isBusy(err) && pending.rows() < maxHeldRows:
			if !held {
				w.opts.Log.Warn("database busy; holding aggregates to write again", "samples", samples)
			}
			held = true
			return
		case isBusy(err):
			w.errors.Add(1)
			w.dropped.Add(samples)
			w.opts.Log.Error("gave up aggregates held for a busy database", "samples", samples)
		case err == nil && held:
			w.opts.Log.Info("aggregates written after the database was busy", "samples", samples)
		}
		held = false
		pending = newFolded()
		samples = 0
	}
	for {
		select {
		case batch, ok := <-w.in:
			if !ok {
				flush()
				return
			}
			newest := latest
			for _, smp := range batch {
				newest = max(newest, smp.Minute)
			}
			if newest > latest {
				// The minutes before it have closed; write them on their own.
				flush()
				latest = newest
			}
			w.fold(&pending, batch)
			samples += int64(len(batch))
			if pending.rows() >= maxPendingRows && !held {
				flush()
			}
		case <-t.C:
			flush()
		}
	}
}

// maxHeldRows bounds the folded rows kept for a busy database.
const maxHeldRows = 20 * maxPendingRows

// write writes one transaction; a busy database leaves f to be tried again,
// any other failure is logged and given up.
func (w *Writer) write(f folded, samples int64) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := w.writeFolded(ctx, f)
	if err == nil {
		w.batches.Add(1)
		w.written.Add(samples)
		return nil
	}
	if !isBusy(err) {
		w.errors.Add(1)
		w.opts.Log.Error("could not write aggregates", "samples", samples, "error", err)
		return nil
	}
	return err
}

type totalKey struct {
	platform, signal string
	minute           int64
}

type dimKey struct {
	totalKey
	dim, value string
}

type pairKey struct {
	totalKey
	dim, value, sub, subValue string
}

type folded struct {
	totals map[totalKey]*state.Agg
	dims   map[dimKey]*state.Agg
	pairs  map[pairKey]*state.Agg
	labels map[labelKey]namedAt
}

func newFolded() folded {
	return folded{
		totals: map[totalKey]*state.Agg{},
		dims:   map[dimKey]*state.Agg{},
		pairs:  map[pairKey]*state.Agg{},
		labels: map[labelKey]namedAt{},
	}
}

func (f folded) rows() int { return len(f.totals) + len(f.dims) + len(f.pairs) + len(f.labels) }

// fold sums a batch into the rows it touches, so thousands of samples become
// one upsert per total, per value and per pair value.
func (w *Writer) fold(into *folded, samples []state.Sample) {
	f := *into
	layouts := map[[2]string]*Layout{}
	newest := int64(0)
	add := func(into **state.Agg, a state.Agg) {
		if *into == nil {
			*into = &state.Agg{}
		}
		(*into).Merge(a)
	}
	for _, s := range samples {
		lk := [2]string{s.Platform, s.Signal}
		layout, ok := layouts[lk]
		if !ok {
			if l, known := w.layoutOf(s); known {
				layout = &l
			}
			layouts[lk] = layout
		}
		if layout == nil {
			continue
		}
		newest = max(newest, s.Minute)
		tk := totalKey{s.Platform, s.Signal, s.Minute}
		t := f.totals[tk]
		add(&t, s.Agg)
		f.totals[tk] = t

		stored := make(map[string]string, len(layout.Dims))
		for _, dim := range layout.Dims {
			value := s.Dims[dim]
			if value == "" {
				continue
			}
			value = w.capped(tk, dim, value, layout.MaxKeys)
			stored[dim] = value
			dk := dimKey{tk, dim, value}
			d := f.dims[dk]
			add(&d, s.Agg)
			f.dims[dk] = d
		}
		for _, p := range layout.Pairs {
			main, sub := stored[p.Main], stored[p.Sub]
			if main == "" || sub == "" {
				continue
			}
			pk := pairKey{tk, p.Main, main, p.Sub, sub}
			a := f.pairs[pk]
			add(&a, s.Agg)
			f.pairs[pk] = a
		}
		for dim, by := range layout.Labels {
			w.name(f.labels, labelDim{s.Platform, s.Signal, dim}, s.Dims[dim], s.Dims[by], s.Minute)
		}
	}
	w.forgetBefore(newest - 10)
}

// name queues a value's name when it is new, has changed, or was last written
// labelRefresh minutes ago.
func (w *Writer) name(into map[labelKey]namedAt, ld labelDim, value, name string, minute int64) {
	if value == "" || name == "" || name == value {
		return
	}
	k := labelKey{ld, value}
	have, ok := w.named[k]
	switch {
	case ok && have.name == name && minute-have.minute < labelRefresh:
		return
	case !ok && w.namedPerDim[ld] >= state.MaxLabelsPerDim:
		return
	case !ok:
		w.namedPerDim[ld]++
	}
	at := namedAt{name: name, minute: minute}
	w.named[k] = at
	into[k] = at
}

func (w *Writer) layoutOf(s state.Sample) (Layout, bool) {
	if w.opts.Layout != nil {
		return w.opts.Layout(s.Platform, s.Signal)
	}
	dims := make([]string, 0, len(s.Dims))
	for d := range s.Dims {
		dims = append(dims, d)
	}
	sort.Strings(dims)
	return Layout{Dims: dims}, true
}

// capped returns the value stored for a dimension in a minute: itself while
// the minute has room, state.OtherBucket after that.
func (w *Writer) capped(tk totalKey, dim, value string, maxKeys int) string {
	if maxKeys <= 0 {
		return value
	}
	ck := capKey{tk.platform, tk.signal, dim, tk.minute}
	values := w.seen[ck]
	if values == nil {
		values = map[string]struct{}{}
		w.seen[ck] = values
	}
	if _, ok := values[value]; ok {
		return value
	}
	if len(values) >= maxKeys {
		return state.OtherBucket
	}
	values[value] = struct{}{}
	return value
}

func (w *Writer) forgetBefore(minute int64) {
	for k := range w.seen {
		if k.minute < minute {
			delete(w.seen, k)
		}
	}
}

func (w *Writer) writeFolded(ctx context.Context, f folded) error {
	tx, err := w.store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	total, err := tx.PrepareContext(ctx, upsertTotal)
	if err != nil {
		return err
	}
	defer func() { _ = total.Close() }()
	dim, err := tx.PrepareContext(ctx, upsertDim)
	if err != nil {
		return err
	}
	defer func() { _ = dim.Close() }()
	pair, err := tx.PrepareContext(ctx, upsertPair)
	if err != nil {
		return err
	}
	defer func() { _ = pair.Close() }()
	label, err := tx.PrepareContext(ctx, upsertLabel)
	if err != nil {
		return err
	}
	defer func() { _ = label.Close() }()

	for k, a := range f.totals {
		if _, err := total.ExecContext(ctx, k.platform, k.signal, k.minute,
			a.Count, a.SumMS, a.MinMS, a.MaxMS, a.Samples); err != nil {
			return fmt.Errorf("total: %w", err)
		}
	}
	for k, a := range f.dims {
		if _, err := dim.ExecContext(ctx, k.platform, k.signal, k.minute, k.dim, k.value,
			a.Count, a.SumMS, a.MinMS, a.MaxMS, a.Samples); err != nil {
			return fmt.Errorf("dimension: %w", err)
		}
	}
	for k, a := range f.pairs {
		if _, err := pair.ExecContext(ctx, k.platform, k.signal, k.minute, k.dim, k.value, k.sub, k.subValue,
			a.Count, a.SumMS, a.MinMS, a.MaxMS, a.Samples); err != nil {
			return fmt.Errorf("pair: %w", err)
		}
	}
	for k, n := range f.labels {
		if _, err := label.ExecContext(ctx, k.platform, k.signal, k.dim, k.value, n.name, n.minute); err != nil {
			return fmt.Errorf("label: %w", err)
		}
	}
	return tx.Commit()
}
