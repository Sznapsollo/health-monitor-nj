/* eslint-disable */
/**
 * GENERATED FILE - do not edit.
 * Source: internal/protocol (Go structs) -> schema/hm-protocol-v1.json -> this file.
 * Regenerate with `make gen`.
 */

/**
 * One JSON object per UDP datagram, or an array of them for batching.
 */
export type HealthMonitorNJWireProtocolV1 = Metric | Gauge | Status | Alert | Log | Chunk

/**
 * Metric is one observation folded into a per-minute cube: dimensions to group by and numeric values to aggregate.
 */
export interface Metric {
  /**
   * Protocol version; always 1
   */
  v: 1
  /**
   * Envelope type
   */
  t: 'metric'
  /**
   * Platform this packet belongs to; resolved from the listener or sender IP when absent
   */
  platform?: string
  /**
   * Host or instance name of the sender
   */
  source?: string
  /**
   * Event time; server time is used when absent
   */
  ts?: string
  /**
   * Optional shared token; unused today but reserved so senders need no rewrite later
   */
  auth?: string
  /**
   * Signal name; for example requests or jobs
   */
  signal: string
  /**
   * Dimension values to group by
   */
  dims?: {
    [k: string]: string
  }
  /**
   * Numeric values; count defaults to 1 when absent
   */
  values?: {
    [k: string]: number
  }
}
/**
 * Gauge is the latest set of labelled values reported by one source.
 */
export interface Gauge {
  /**
   * Protocol version; always 1
   */
  v: 1
  /**
   * Envelope type
   */
  t: 'gauge'
  /**
   * Platform this packet belongs to; resolved from the listener or sender IP when absent
   */
  platform?: string
  /**
   * Host or instance name of the sender
   */
  source?: string
  /**
   * Event time; server time is used when absent
   */
  ts?: string
  /**
   * Optional shared token; unused today but reserved so senders need no rewrite later
   */
  auth?: string
  signal: string
  points: GaugePoint[]
}
/**
 * GaugePoint is one labelled reading of a gauge.
 */
export interface GaugePoint {
  label: string
  value: number
  warn?: boolean
  unit?: string
}
/**
 * Status is the current state of one keyed entity, refreshed by a heartbeat.
 */
export interface Status {
  /**
   * Protocol version; always 1
   */
  v: 1
  /**
   * Envelope type
   */
  t: 'status'
  /**
   * Platform this packet belongs to; resolved from the listener or sender IP when absent
   */
  platform?: string
  /**
   * Host or instance name of the sender
   */
  source?: string
  /**
   * Event time; server time is used when absent
   */
  ts?: string
  /**
   * Optional shared token; unused today but reserved so senders need no rewrite later
   */
  auth?: string
  signal: string
  /**
   * Identity of the reporting entity; for example a server name
   */
  key: string
  payload?: {}
  /**
   * The whole report to keep when the signal is an info signal
   */
  content?: {}
}
/**
 * Alert is a condition a human may need to look at.
 */
export interface Alert {
  /**
   * Protocol version; always 1
   */
  v: 1
  /**
   * Envelope type
   */
  t: 'alert'
  /**
   * Platform this packet belongs to; resolved from the listener or sender IP when absent
   */
  platform?: string
  /**
   * Host or instance name of the sender
   */
  source?: string
  /**
   * Event time; server time is used when absent
   */
  ts?: string
  /**
   * Optional shared token; unused today but reserved so senders need no rewrite later
   */
  auth?: string
  /**
   * Alert stream name; defaults to alerts
   */
  signal?: string
  level: 'ERROR' | 'WARN' | 'INFO' | 'DEBUG' | 'TRACE'
  /**
   * Grouping label chosen by the platform
   */
  category?: string
  message: string
  /**
   * Repeats sharing this key within one minute collapse into a single entry
   */
  groupKey?: string
  data?: {}
}
/**
 * Log is one searchable row.
 */
export interface Log {
  /**
   * Protocol version; always 1
   */
  v: 1
  /**
   * Envelope type
   */
  t: 'log'
  /**
   * Platform this packet belongs to; resolved from the listener or sender IP when absent
   */
  platform?: string
  /**
   * Host or instance name of the sender
   */
  source?: string
  /**
   * Event time; server time is used when absent
   */
  ts?: string
  /**
   * Optional shared token; unused today but reserved so senders need no rewrite later
   */
  auth?: string
  signal: string
  /**
   * Optional grouping key; for example a day bucket or a recipient
   */
  key?: string
  level?: 'ERROR' | 'WARN' | 'INFO' | 'DEBUG' | 'TRACE'
  data?: {}
}
/**
 * Chunk carries one part of a payload too large for a single datagram.
 */
export interface Chunk {
  /**
   * Protocol version; always 1
   */
  v: 1
  /**
   * Envelope type
   */
  t: 'chunk'
  /**
   * Platform this packet belongs to; resolved from the listener or sender IP when absent
   */
  platform?: string
  /**
   * Host or instance name of the sender
   */
  source?: string
  /**
   * Event time; server time is used when absent
   */
  ts?: string
  /**
   * Optional shared token; unused today but reserved so senders need no rewrite later
   */
  auth?: string
  id: string
  part: number
  of: number
  /**
   * Slice of the UTF-8 JSON payload
   */
  data: string
}
