// The live-update protocol, mirroring internal/hub. The packet envelopes in
// src/protocol.ts are generated from the Go structs; these are the messages a
// viewer exchanges with the hub.

export interface Point {
  minute: number
  count: number
  avgMs: number
  minMs: number
  maxMs: number
}

export interface Group {
  value: string
  /** The value's name, when the signal labels this dimension. */
  label?: string
  count: number
  avgMs: number
  points: Point[]
  groups?: Group[]
}

export interface SignalView {
  platform: string
  signal: string
  from: number
  to: number
  total: Point[]
  group?: string
  sub?: string
  groups?: Group[]
  /** First minute that still has dimension breakdowns in memory. */
  detailFrom: number
  /** Values folded into the __other__ bucket, per dimension. */
  capped?: Record<string, number>
  /** The group breakdown covers only part of the window. */
  partial?: boolean
  groupsFrom?: number
  /** The main chart counts only what the group filter keeps. */
  filtered?: boolean
}

export interface Criteria {
  /** Tells two views of one signal apart; the server defaults it to the signal. */
  id?: string
  signal: string
  historyMinutes?: number
  /** The group tiles' own window; the server uses 120 when it is not given. */
  groupMinutes?: number
  group?: string
  groupTop?: number
  groupFilter?: string
  sub?: string
  subTop?: number
  subFilter?: string
  /** Sub-groups stack unless this is false. */
  stacked?: boolean
  /** The main chart shows unless this is false and a group is set. */
  main?: boolean
  /** Draws every group in one stacked chart instead of a tile each. */
  together?: boolean
  sortBy?: 'count' | 'avgMs'
}

export interface ClientMessage {
  t: 'hello' | 'criteria' | 'ping'
  name?: string
  platform?: string
  criteria?: Criteria[]
  /** Asks for one 'events' message per flush instead of one 'event' per chart and minute. */
  batch?: boolean
  /** This browser tab, the same across reconnects and reloads. */
  tab?: string
}

export interface MinuteEvent {
  id: string
  signal: string
  minute: number
  view: SignalView
}

export interface ServerMessage {
  t: 'snapshot' | 'event' | 'events' | 'alert' | 'notice' | 'pong' | 'error'
  serverTime?: string
  platforms?: string[]
  signals?: Record<string, SignalView>
  id?: string
  signal?: string
  minute?: number
  view?: SignalView
  events?: MinuteEvent[]
  /** Alert: one alert as it is raised. */
  alert?: import('../api/alerts').Alert
  /** New says it is not a repeat, so it may deserve a sound. */
  new?: boolean

  level?: string
  text?: string
  popup?: boolean
}

/** The bucket the server folds capped dimension values into. */
export const OTHER_BUCKET = '__other__'
