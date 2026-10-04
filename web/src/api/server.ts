import { apiFetch } from './http'

export interface ResourceSample {
  at: string
  rssBytes: number
  cpuPercent: number
  packetsPerSec?: number
}

/** One disk the monitor writes to, with the folders on it. */
export interface DiskReading {
  folders: string[]
  freeBytes: number
  totalBytes: number
  /** Below data.min_free_gb. */
  low: boolean
}

export function gigabytes(bytes: number): string {
  return `${(bytes / 1024 ** 3).toFixed(1)} GB`
}

export interface ServerResources {
  rssBytes: number
  heapBytes: number
  memLimitBytes?: number
  cpuPercent: number
  cpuPercent1m: number
  packetsPerSec?: number
  packetsLastMinute?: number
  logsLastMinute?: number
  cores: number
  goroutines: number
  openFiles?: number
  gcCycles: number
  started: string
  uptimeSeconds: number
  dbBytes?: number
  disks?: DiskReading[]
  history?: ResourceSample[]
}

export async function fetchServer(history: boolean): Promise<ServerResources> {
  const res = await apiFetch(`/api/server${history ? '?history=1' : ''}`)
  if (!res.ok) throw new Error(`/api/server responded ${res.status}`)
  return (await res.json()) as ServerResources
}

export function megabytes(bytes: number): string {
  return bytes >= 10 * 1024 ** 3
    ? `${(bytes / 1024 ** 3).toFixed(1)} GB`
    : `${Math.round(bytes / 1024 ** 2)} MB`
}

export function perSecond(rate: number): string {
  return rate >= 10 ? Math.round(rate).toLocaleString() : String(rate)
}

/** Memory is loud past 80 % of a container limit, CPU past 80 % of the cores it may use. */
export function strained(r: ServerResources): { memory: boolean; cpu: boolean } {
  return {
    memory: Boolean(r.memLimitBytes) && r.rssBytes > 0.8 * (r.memLimitBytes ?? 0),
    cpu: r.cpuPercent1m > 80 * Math.max(r.cores, 1),
  }
}

export interface BuildVersion {
  version: string
  commit: string
  goVersion: string
}

export async function fetchVersion(signal?: AbortSignal): Promise<BuildVersion> {
  const res = await apiFetch('/api/version', { signal })
  if (!res.ok) throw new Error(`/api/version responded ${res.status}`)
  return (await res.json()) as BuildVersion
}
