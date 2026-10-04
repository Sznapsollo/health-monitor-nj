import { apiFetch, readJSON } from './http'

export interface InfoEntry {
  signal: string
  key: string
  displayName: string
  packetType?: string
  merge?: boolean
  noStatus?: boolean
  defined: boolean
  offline?: boolean
  received: string
  versions: number
}

export interface InfoVersion {
  id: number
  received: string
  size: number
}

export interface InfoReport {
  id: number
  received: string
  content: unknown
  versions: InfoVersion[]
}

export async function fetchInfoEntries(
  platform: string,
  signal?: AbortSignal,
): Promise<InfoEntry[]> {
  const res = await apiFetch(`/api/info?platform=${encodeURIComponent(platform)}`, { signal })
  const body = await readJSON<{ entries: InfoEntry[] | null }>('info', res)
  return body.entries ?? []
}

/** Deletes every kept report of one sender, and its row on the Status tab. */
export async function forgetInfo(
  platform: string,
  entry: { signal: string; key: string },
): Promise<void> {
  const params = new URLSearchParams({ platform, signal: entry.signal, key: entry.key })
  const res = await apiFetch(`/api/info/report?${params.toString()}`, { method: 'DELETE' })
  if (!res.ok) throw new Error(`info report responded ${res.status}`)
}

export async function fetchInfoReport(
  platform: string,
  entry: { signal: string; key: string },
  id: number | null,
  signal?: AbortSignal,
): Promise<InfoReport> {
  const params = new URLSearchParams({ platform, signal: entry.signal, key: entry.key })
  if (id !== null) params.set('id', String(id))
  const res = await apiFetch(`/api/info/report?${params.toString()}`, { signal })
  return readJSON<InfoReport>('info report', res)
}
