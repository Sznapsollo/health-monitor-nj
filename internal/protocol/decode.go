package protocol

import (
	"encoding/json"
	"fmt"
)

// Packet is a decoded v1 datagram: the common header plus exactly one payload.
type Packet struct {
	Header
	Metric *Metric
	Gauge  *Gauge
	Status *Status
	Alert  *Alert
	Log    *Log
	Chunk  *Chunk
}

// Signal returns the signal name the packet belongs to, empty for chunks.
func (p Packet) Signal() string {
	switch {
	case p.Metric != nil:
		return p.Metric.Signal
	case p.Gauge != nil:
		return p.Gauge.Signal
	case p.Status != nil:
		return p.Status.Signal
	case p.Alert != nil:
		return p.Alert.Signal
	case p.Log != nil:
		return p.Log.Signal
	}
	return ""
}

// ErrLegacy is returned for a datagram without a version field: protocol v0,
// which belongs to a platform adapter, not to the core.
var ErrLegacy = fmt.Errorf("protocol: no version field (v0)")

// Decode decodes one v1 envelope.
func Decode(b []byte) (Packet, error) {
	var h Header
	if err := json.Unmarshal(b, &h); err != nil {
		return Packet{}, fmt.Errorf("protocol: decode header: %w", err)
	}
	if h.V == 0 {
		return Packet{}, ErrLegacy
	}
	if h.V != Version {
		return Packet{}, fmt.Errorf("protocol: unsupported version %d", h.V)
	}
	p := Packet{Header: h}
	var err error
	switch h.T {
	case TypeMetric:
		p.Metric = &Metric{}
		err = json.Unmarshal(b, p.Metric)
	case TypeGauge:
		p.Gauge = &Gauge{}
		err = json.Unmarshal(b, p.Gauge)
	case TypeStatus:
		p.Status = &Status{}
		err = json.Unmarshal(b, p.Status)
	case TypeAlert:
		p.Alert = &Alert{}
		err = json.Unmarshal(b, p.Alert)
	case TypeLog:
		p.Log = &Log{}
		err = json.Unmarshal(b, p.Log)
	case TypeChunk:
		p.Chunk = &Chunk{}
		err = json.Unmarshal(b, p.Chunk)
	default:
		return Packet{}, fmt.Errorf("protocol: unknown envelope type %q", h.T)
	}
	if err != nil {
		return Packet{}, fmt.Errorf("protocol: decode %s: %w", h.T, err)
	}
	return p, nil
}

// DecodeBatch decodes a datagram holding either one envelope or a JSON array
// of them.
func DecodeBatch(b []byte) ([]Packet, error) {
	for _, c := range b {
		switch c {
		case ' ', '\t', '\r', '\n':
			continue
		case '[':
			var raw []json.RawMessage
			if err := json.Unmarshal(b, &raw); err != nil {
				return nil, fmt.Errorf("protocol: decode batch: %w", err)
			}
			out := make([]Packet, 0, len(raw))
			for i, r := range raw {
				p, err := Decode(r)
				if err != nil {
					return nil, fmt.Errorf("protocol: batch item %d: %w", i, err)
				}
				out = append(out, p)
			}
			return out, nil
		}
		break
	}
	p, err := Decode(b)
	if err != nil {
		return nil, err
	}
	return []Packet{p}, nil
}
