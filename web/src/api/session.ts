import { apiFetch } from './http'

export interface SessionInfo {
  required: boolean
  authenticated: boolean
  readOnly: boolean
  name?: string
  kind?: 'user' | 'display'
  expires?: string
  /** The arrangement a wall display's token is paired with, if it is one. */
  platform?: string
  dashboard?: string
  /** How a wall display draws itself; only for a display. */
  display?: DisplayOptions
  /** The password is still the one a fresh install ships with. */
  defaultPassword?: boolean
}

/** How a wall display draws itself, beyond what it shows. */
export interface DisplayOptions {
  showSystem?: boolean
  showLive?: boolean
  /**
   * Absent follows the screen's own system setting; 'screen' puts a switch on
   * the screen, starting from that.
   */
  theme?: 'light' | 'dark' | 'screen'
}

export interface DisplayToken {
  id: string
  name: string
  /** The platform and dashboard this screen shows. */
  platform?: string
  dashboard?: string
  options?: DisplayOptions
  created: string
  lastUsed?: string
  /** Only ever returned once, when the token is created. */
  secret?: string
}

export async function fetchSession(signal?: AbortSignal): Promise<SessionInfo> {
  const res = await apiFetch('/api/session', { signal })
  if (!res.ok) throw new Error(`session responded ${res.status}`)
  return (await res.json()) as SessionInfo
}

export async function login(name: string, password: string): Promise<SessionInfo> {
  const res = await apiFetch('/api/login', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name, password }),
  })
  if (!res.ok) {
    const problem = (await res.json().catch(() => ({}))) as { error?: string }
    throw new Error(problem.error ?? 'that did not work')
  }
  return (await res.json()) as SessionInfo
}

export async function logout(): Promise<void> {
  await apiFetch('/api/logout', { method: 'POST' })
}

export async function fetchDisplayTokens(signal?: AbortSignal): Promise<DisplayToken[]> {
  const res = await apiFetch('/api/display-tokens', { signal })
  if (!res.ok) throw new Error(`display tokens responded ${res.status}`)
  const body = (await res.json()) as { tokens: DisplayToken[] | null }
  return body.tokens ?? []
}

export async function createDisplayToken(
  name: string,
  platform: string,
  dashboard: string,
  options: DisplayOptions = {},
): Promise<DisplayToken> {
  const res = await apiFetch('/api/display-tokens', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name, platform, dashboard, options }),
  })
  if (!res.ok) {
    const problem = (await res.json().catch(() => ({}))) as { error?: string }
    throw new Error(problem.error ?? `the server answered ${res.status}`)
  }
  return (await res.json()) as DisplayToken
}

/** Changes how a screen draws itself; it keeps its token and link. */
export async function updateDisplayOptions(id: string, options: DisplayOptions): Promise<void> {
  const res = await apiFetch(`/api/display-tokens/${encodeURIComponent(id)}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(options),
  })
  if (!res.ok) throw new Error(`the server answered ${res.status}`)
}

export async function revokeDisplayToken(id: string): Promise<void> {
  const res = await apiFetch(`/api/display-tokens/${encodeURIComponent(id)}`, { method: 'DELETE' })
  if (!res.ok) throw new Error(`the server answered ${res.status}`)
}

export async function sendMessage(text: string, popup: boolean): Promise<void> {
  const res = await apiFetch('/api/message', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ text, popup, level: 'info' }),
  })
  if (!res.ok) throw new Error(`the server answered ${res.status}`)
}

export interface ViewerSession {
  id: string
  name: string
  platform: string
  signals: string[]
  connected: string
  lastSeen: string
  sent: number
  /** The login, or the wall display's name. */
  login?: string
  kind?: 'user' | 'display'
  ip?: string
  userAgent?: string
}

/** One browser tab or wall screen's time with the monitor open. */
export interface Visit {
  id: string
  login?: string
  kind?: 'user' | 'display'
  ip?: string
  userAgent?: string
  signals: string[]
  started: string
  lastSeen: string
  ended?: string
  reconnects: number
  sent: number
  open: boolean
}

export async function fetchVisitDays(signal?: AbortSignal): Promise<string[]> {
  const res = await apiFetch('/api/visits/days', { signal })
  if (!res.ok) throw new Error(`visit days responded ${res.status}`)
  const body = (await res.json()) as { days: string[] | null }
  return body.days ?? []
}

export async function fetchVisits(day: string, signal?: AbortSignal): Promise<Visit[]> {
  const res = await apiFetch(`/api/visits?day=${encodeURIComponent(day)}`, { signal })
  if (!res.ok) throw new Error(`visits responded ${res.status}`)
  const body = (await res.json()) as { visits: Visit[] | null }
  return body.visits ?? []
}

export async function fetchViewers(signal?: AbortSignal): Promise<ViewerSession[]> {
  const res = await apiFetch('/api/sessions', { signal })
  if (!res.ok) throw new Error(`sessions responded ${res.status}`)
  const body = (await res.json()) as { sessions: ViewerSession[] | null }
  return body.sessions ?? []
}

export interface GaugeView {
  platform: string
  signal: string
  points: { label: string; value: number; warn?: boolean; unit?: string }[]
  sources: string[]
  updatedAt?: string
}

export async function fetchGauges(platform: string, signal?: AbortSignal): Promise<GaugeView[]> {
  const res = await apiFetch(`/api/gauges?platform=${encodeURIComponent(platform)}`, { signal })
  if (!res.ok) throw new Error(`gauges responded ${res.status}`)
  const body = (await res.json()) as { gauges: GaugeView[] | null }
  return body.gauges ?? []
}
