// Package state holds the hot, in-memory view of every signal: per-minute
// cubes in two tiers (full detail, then totals only), with the trimming the
// browser needs. Everything older or deeper comes from SQLite.
package state

import "math"

// OtherBucket collects the dimension values that did not fit under a signal's
// cardinality cap, so a capped chart still adds up.
const OtherBucket = "__other__"

// Agg is what one or many reports fold into.
//
// Count and Samples are deliberately different things: one `jobReport` says
// "I handled 3 items and it took 50 ms", which is 3 items but a single
// measurement of 50 ms. Averaging over items instead of measurements would
// quietly triple the weight of that one timing. Senders that pre-aggregate
// send their own Samples along with the summed duration.
type Agg struct {
	Count   int64
	SumMS   float64
	MinMS   float64
	MaxMS   float64
	Samples int64
}

// Add folds one report of count items that took ms milliseconds.
func (a *Agg) Add(count int64, ms float64, hasMS bool) {
	a.AddSummed(count, ms, 1, ms, ms, hasMS)
}

// AddSummed folds a report that already aggregates several measurements:
// count items, sumMS milliseconds across samples measurements.
func (a *Agg) AddSummed(count int64, sumMS float64, samples int64, minMS, maxMS float64, hasMS bool) {
	a.Count += count
	if !hasMS || samples <= 0 {
		return
	}
	if a.Samples == 0 || minMS < a.MinMS {
		a.MinMS = minMS
	}
	if a.Samples == 0 || maxMS > a.MaxMS {
		a.MaxMS = maxMS
	}
	a.SumMS += sumMS
	a.Samples += samples
}

// Merge folds b into a.
func (a *Agg) Merge(b Agg) {
	if b.Count == 0 && b.Samples == 0 {
		return
	}
	a.Count += b.Count
	if b.Samples == 0 {
		return
	}
	if a.Samples == 0 || b.MinMS < a.MinMS {
		a.MinMS = b.MinMS
	}
	if a.Samples == 0 || b.MaxMS > a.MaxMS {
		a.MaxMS = b.MaxMS
	}
	a.SumMS += b.SumMS
	a.Samples += b.Samples
}

// AvgMS is the mean duration per measurement, or zero when the signal carries
// no durations.
func (a Agg) AvgMS() float64 {
	if a.Samples == 0 {
		return 0
	}
	return a.SumMS / float64(a.Samples)
}

// Value returns the number a chart asks for by name: "count" or "avgMs",
// with "minMs", "maxMs" and "sumMs" available for tables.
func (a Agg) Value(name string) float64 {
	switch name {
	case "count", "":
		return float64(a.Count)
	case "avgMs", "avgExecutionTime":
		return a.AvgMS()
	case "minMs":
		return a.MinMS
	case "maxMs":
		return a.MaxMS
	case "sumMs":
		return a.SumMS
	}
	return math.NaN()
}
