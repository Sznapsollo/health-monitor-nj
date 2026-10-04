import { apiFetch } from './http'

export type Level = 'ERROR' | 'WARN' | 'INFO' | 'DEBUG' | 'TRACE'

export interface Alert {
  id: string
  platform: string
  signal?: string
  level: Level
  category?: string
  message: string
  groupKey?: string
  source?: string
  count: number
  first: string
  last: string
  data?: Record<string, unknown>
  target?: string
  silenced?: boolean
  silenceReason?: string
}

export interface AlertsResponse {
  platform: string
  alerts: Alert[]
  categories: string[]
  counts: Partial<Record<Level, number>>
  /** History only: the day shown, and the days the history holds. */
  day?: string
  days?: string[]
  history?: boolean
}

export interface StatusEntity {
  platform: string
  signal: string
  key: string
  payload?: Record<string, unknown>
  lastSeen: string
  offline: boolean
  offlineSince?: string
}

export interface StatusResponse {
  platform: string
  entities: StatusEntity[]
  online: number
  offline: number
  /** Packets each sender sent in the last complete minute, by its name. */
  packets?: Record<string, number>
}

export type SilenceKind = 'snooze' | 'mute'

export interface Silence {
  id: string
  platform: string
  target: string
  kind: SilenceKind
  reason: string
  by: string
  created: string
  until?: string
  suppressed: number
}

export interface SilencesResponse {
  platform: string
  silences: Silence[]
  review: Silence[] | null
}

export interface RuleOverride {
  path?: string
  prefix?: string
  glob?: string
  ms?: number
  level?: string
  ignore?: boolean
  account?: string
  user?: string
  serverName?: string
}

export interface Rules {
  platform: string
  latency: { defaultMs: number; level: string; overrides?: RuleOverride[] }
  /** repeatSeconds: how often a server still down is alerted again; 0 once. */
  offline: { afterSeconds: number; repeatSeconds?: number }
  groups: { windowSeconds: number }
  mute?: { contains: string; level?: string }[]
}

export interface RulesTest {
  platform: string
  minutes: number
  rows: number
  total: number
  byRule: { rule: string; count: number }[] | null
  examples:
    | {
        url: string
        ms: number
        limitMs: number
        rule: string
        level: string
        minute: number
        requests: number
      }[]
    | null
}

export interface AlertQuery {
  platform?: string
  levels?: Level[]
  categories?: string[]
  text?: string
  minMs?: number
  minutes?: number
  limit?: number
  silenced?: boolean
  /** A day as YYYYMMDD reads the history on disk instead of the live window. */
  day?: string
}

function query(q: AlertQuery): string {
  const params = new URLSearchParams()
  if (q.platform) params.set('platform', q.platform)
  if (q.levels?.length) params.set('levels', q.levels.join(','))
  if (q.categories?.length) params.set('categories', q.categories.join(','))
  if (q.text) params.set('text', q.text)
  if (q.minMs) params.set('minMs', String(q.minMs))
  if (q.minutes) params.set('minutes', String(q.minutes))
  if (q.limit) params.set('limit', String(q.limit))
  if (q.silenced) params.set('silenced', 'true')
  if (q.day) params.set('day', q.day)
  return params.toString()
}

async function getJSON<T>(url: string, signal?: AbortSignal): Promise<T> {
  const res = await apiFetch(url, { signal })
  if (!res.ok) throw new Error(`${url} responded ${res.status}`)
  return (await res.json()) as T
}

export function fetchAlerts(q: AlertQuery, signal?: AbortSignal): Promise<AlertsResponse> {
  return getJSON<AlertsResponse>(`/api/alerts?${query(q)}`, signal)
}

/** The URL the export button points at, so the browser does the download. */
export function alertsCsvUrl(q: AlertQuery): string {
  return `/api/alerts?${query(q)}&format=csv`
}

export function fetchStatus(platform: string, signal?: AbortSignal): Promise<StatusResponse> {
  return getJSON<StatusResponse>(`/api/status?platform=${encodeURIComponent(platform)}`, signal)
}

export function fetchSilences(platform: string, signal?: AbortSignal): Promise<SilencesResponse> {
  return getJSON<SilencesResponse>(`/api/silences?platform=${encodeURIComponent(platform)}`, signal)
}

export async function createSilence(body: {
  platform: string
  target: string
  kind: SilenceKind
  reason: string
  by?: string
  minutes?: number
}): Promise<Silence> {
  const res = await apiFetch('/api/silences', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
  if (!res.ok) {
    const problem = (await res.json().catch(() => ({}))) as { error?: string }
    throw new Error(problem.error ?? `the server answered ${res.status}`)
  }
  return (await res.json()) as Silence
}

/** Changes what a silence covers, how long it lasts and why; it keeps its history. */
export async function updateSilence(
  id: string,
  body: { target: string; kind: SilenceKind; reason: string; by?: string; minutes?: number },
): Promise<Silence> {
  const res = await apiFetch(`/api/silences/${encodeURIComponent(id)}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
  if (!res.ok) {
    const problem = (await res.json().catch(() => ({}))) as { error?: string }
    throw new Error(problem.error ?? `the server answered ${res.status}`)
  }
  return (await res.json()) as Silence
}

export async function deleteSilence(id: string): Promise<void> {
  const res = await apiFetch(`/api/silences/${encodeURIComponent(id)}`, { method: 'DELETE' })
  if (!res.ok) throw new Error(`the server answered ${res.status}`)
}

export async function forgetEntity(body: {
  platform: string
  signal: string
  key: string
}): Promise<void> {
  const res = await apiFetch('/api/status/forget', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
  if (!res.ok) throw new Error(`the server answered ${res.status}`)
}

export function fetchRules(platform: string, signal?: AbortSignal): Promise<Rules> {
  return getJSON<Rules>(`/api/rules?platform=${encodeURIComponent(platform)}`, signal)
}

export async function saveRules(platform: string, rules: Rules): Promise<Rules> {
  const res = await apiFetch(`/api/rules?platform=${encodeURIComponent(platform)}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(rules),
  })
  if (!res.ok) {
    const problem = (await res.json().catch(() => ({}))) as { error?: string }
    throw new Error(problem.error ?? `the server answered ${res.status}`)
  }
  return (await res.json()) as Rules
}

export async function testRules(platform: string, rules?: Rules): Promise<RulesTest> {
  const res = await apiFetch(`/api/rules/test?platform=${encodeURIComponent(platform)}`, {
    method: 'POST',
    headers: rules ? { 'Content-Type': 'application/json' } : undefined,
    body: rules ? JSON.stringify(rules) : undefined,
  })
  if (!res.ok) throw new Error(`the server answered ${res.status}`)
  return (await res.json()) as RulesTest
}
