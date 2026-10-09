import { apiFetch } from './http'

export interface Candidate {
  platform: string
  signal: string
  kind: 'metric' | 'gauge' | 'log' | 'info'
  count: number
  first: string
  last: string
  dims: Record<string, string[]>
  values: Record<string, { min: number; max: number }>
  minutes: { minute: number; packets: number; sums: Record<string, number> }[]
}

export interface SignalEdit {
  kind: 'timeseries' | 'gauge' | 'log' | 'info'
  displayName: string
  dims: { name: string; displayName: string; labelDim?: string }[]
  values: { count: string; ms: string }
  retention: {
    hotDetailMinutes: number
    hotTotalsMinutes: number
    durableDays: number
    detailDays: number
    /** A log signal's days in the database; absent leaves it to the configuration. */
    logDays?: number
    /** A log signal's days as archive files; absent leaves it to the configuration. */
    archiveDays?: number
    /** An info signal's reports kept per sender. */
    versions?: number
  }
  /** The sender's packet type an info signal keeps. */
  packetType?: string
  merge?: boolean
  noStatus?: boolean
  colors?: Record<string, string>
  keepPairs: [string, string][]
  maxKeys: number
  ttlSeconds: number
  groupTop: number
}

async function problem(res: Response): Promise<Error> {
  const body = (await res.json().catch(() => ({}))) as { error?: string }
  return new Error(body.error ?? `the server answered ${res.status}`)
}

export async function fetchCandidates(signal?: AbortSignal): Promise<Candidate[]> {
  const res = await apiFetch('/api/signals/candidates', { signal })
  if (!res.ok) throw await problem(res)
  const body = (await res.json()) as { candidates: Candidate[] | null }
  return body.candidates ?? []
}

function signalUrl(platform: string, name: string): string {
  return `/api/signals/${encodeURIComponent(name)}?platform=${encodeURIComponent(platform)}`
}

export async function saveSignal(platform: string, name: string, edit: SignalEdit): Promise<void> {
  const res = await apiFetch(signalUrl(platform, name), {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(edit),
  })
  if (!res.ok) throw await problem(res)
}

/** What config.yaml alone gives a kind of log: what an empty field falls back to. */
export async function fetchLogRetention(
  kind: string,
): Promise<{ dbDays: number; archiveDays: number }> {
  const res = await apiFetch(`/api/logs/retention?kind=${encodeURIComponent(kind)}`)
  if (!res.ok) throw await problem(res)
  return (await res.json()) as { dbDays: number; archiveDays: number }
}

/** What an undefined signal arrived with, fetched only when asked for. */
export async function fetchCandidateSample(c: Candidate): Promise<unknown> {
  const params = new URLSearchParams({ platform: c.platform, kind: c.kind })
  const res = await apiFetch(
    `/api/signals/candidates/${encodeURIComponent(c.signal)}/sample?${params.toString()}`,
  )
  if (!res.ok) throw await problem(res)
  const body = (await res.json()) as { sample: unknown }
  return body.sample
}

/** Removes a signal that arrived undefined; a log loses every stored day. */
export async function dismissCandidate(c: Candidate): Promise<void> {
  const params = new URLSearchParams({ platform: c.platform, kind: c.kind })
  const res = await apiFetch(
    `/api/signals/candidates/${encodeURIComponent(c.signal)}?${params.toString()}`,
    { method: 'DELETE' },
  )
  if (!res.ok) throw await problem(res)
}

export function signalsExportUrl(platform: string): string {
  return `/api/signals/export?platform=${encodeURIComponent(platform)}`
}

export interface SignalsImport {
  imported: string[]
  skipped: string[]
}

/** Defines the file's signals the platform lacks; the ones it has are left alone. */
export async function importSignals(platform: string, text: string): Promise<SignalsImport> {
  const res = await apiFetch(`/api/signals/import?platform=${encodeURIComponent(platform)}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ text }),
  })
  if (!res.ok) throw await problem(res)
  return (await res.json()) as SignalsImport
}

export async function deleteSignal(platform: string, name: string): Promise<void> {
  const res = await apiFetch(signalUrl(platform, name), { method: 'DELETE' })
  if (!res.ok) throw await problem(res)
}
