// Package protocol defines wire protocol v1: the JSON envelopes any platform
// sends to healthMonitorNJ over UDP. These structs are the single source of
// truth; schema/hm-protocol-v1.json and web/src/protocol.ts are generated from
// them by `make gen`.
package protocol

// Version is the protocol version carried in every v1 envelope. A datagram
// without a version field is protocol v0 (legacy) and is handled by a platform
// adapter before the core sees it.
const Version = 1

// Type discriminates the envelope payload.
type Type string

const (
	TypeMetric Type = "metric"
	TypeGauge  Type = "gauge"
	TypeStatus Type = "status"
	TypeAlert  Type = "alert"
	TypeLog    Type = "log"
	TypeChunk  Type = "chunk"
)

// Level is the severity of an alert or log line.
type Level string

const (
	LevelError Level = "ERROR"
	LevelWarn  Level = "WARN"
	LevelInfo  Level = "INFO"
	LevelDebug Level = "DEBUG"
	LevelTrace Level = "TRACE"
)

// Header carries the fields common to every envelope.
type Header struct {
	V        int       `json:"v" jsonschema:"enum=1,description=Protocol version; always 1"`
	T        Type      `json:"t" jsonschema:"enum=metric,enum=gauge,enum=status,enum=alert,enum=log,enum=chunk,description=Envelope type"`
	Platform string    `json:"platform,omitempty" jsonschema:"description=Platform this packet belongs to; resolved from the listener or sender IP when absent"`
	Source   string    `json:"source,omitempty" jsonschema:"description=Host or instance name of the sender"`
	TS       Timestamp `json:"ts,omitempty" jsonschema:"description=Event time; server time is used when absent"`
	Auth     string    `json:"auth,omitempty" jsonschema:"description=Optional shared token; unused today but reserved so senders need no rewrite later"`
}

// Metric is one observation folded into a per-minute cube: dimensions to group
// by and numeric values to aggregate.
type Metric struct {
	Header
	Signal string             `json:"signal" jsonschema:"description=Signal name; for example requests or jobs"`
	Dims   map[string]string  `json:"dims,omitempty" jsonschema:"description=Dimension values to group by"`
	Values map[string]float64 `json:"values,omitempty" jsonschema:"description=Numeric values; count defaults to 1 when absent"`
}

// GaugePoint is one labelled reading of a gauge.
type GaugePoint struct {
	Label string  `json:"label"`
	Value float64 `json:"value"`
	Warn  bool    `json:"warn,omitempty"`
	Unit  string  `json:"unit,omitempty"`
}

// Gauge is the latest set of labelled values reported by one source.
type Gauge struct {
	Header
	Signal string       `json:"signal"`
	Points []GaugePoint `json:"points"`
}

// Status is the current state of one keyed entity, refreshed by a heartbeat.
type Status struct {
	Header
	Signal  string         `json:"signal"`
	Key     string         `json:"key" jsonschema:"description=Identity of the reporting entity; for example a server name"`
	Payload map[string]any `json:"payload,omitempty"`
	Content map[string]any `json:"content,omitempty" jsonschema:"description=The whole report to keep when the signal is an info signal"`
}

// Alert is a condition a human may need to look at.
type Alert struct {
	Header
	Signal   string         `json:"signal,omitempty" jsonschema:"description=Alert stream name; defaults to alerts"`
	Level    Level          `json:"level" jsonschema:"enum=ERROR,enum=WARN,enum=INFO,enum=DEBUG,enum=TRACE"`
	Category string         `json:"category,omitempty" jsonschema:"description=Grouping label chosen by the platform"`
	Message  string         `json:"message"`
	GroupKey string         `json:"groupKey,omitempty" jsonschema:"description=Repeats sharing this key within one minute collapse into a single entry"`
	Data     map[string]any `json:"data,omitempty"`
}

// Log is one searchable row.
type Log struct {
	Header
	Signal string         `json:"signal"`
	Key    string         `json:"key,omitempty" jsonschema:"description=Optional grouping key; for example a day bucket or a recipient"`
	Level  Level          `json:"level,omitempty" jsonschema:"enum=ERROR,enum=WARN,enum=INFO,enum=DEBUG,enum=TRACE"`
	Data   map[string]any `json:"data,omitempty"`
}

// Chunk carries one part of a payload too large for a single datagram. The
// parts are reassembled by their id before decoding.
type Chunk struct {
	Header
	ID   string `json:"id"`
	Part int    `json:"part" jsonschema:"minimum=1"`
	Of   int    `json:"of" jsonschema:"minimum=1"`
	Data string `json:"data" jsonschema:"description=Slice of the UTF-8 JSON payload"`
}
