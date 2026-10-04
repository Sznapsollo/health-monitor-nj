import { apiFetch, readJSON } from './http'
import type { DiskReading } from './server'

export interface LogRow {
  platform: string
  signal: string
  ts: string
  key?: string
  account?: string
  user?: string
  url?: string
  level?: string
  payload?: Record<string, unknown>
}

export interface SearchResponse {
  platform: string
  rows: LogRow[] | null
  count: number
  kinds: string[]
}

export interface SearchQuery {
  platform?: string
  kind?: string
  text?: string
  account?: string
  user?: string
  days?: number
  limit?: number
}

export interface HistoryDay {
  day: string
  rows: number
  bytes: number
  /** Still being written, so it cannot be archived or deleted. */
  today?: boolean
  /** When each kind of log in it leaves the database, as the configuration stands. */
  leaves?: Leaving[]
  /** The size is worked out from the rows: today is still growing. */
  estimated?: boolean
  /** Not counted yet since the monitor started; the numbers follow shortly. */
  measuring?: boolean
}

export interface Leaving {
  kind: string
  name?: string
  on: string
  /** Becomes an archive file; false drops it. */
  archive: boolean
}

export interface ArchiveFile {
  file: string
  day: string
  kind: string
  bytes: number
  /** When the file was written; its keep period counts from here. */
  written: string
  /** When the monitor will delete it; absent when it is kept for good. */
  deleteOn?: string
  alerts?: boolean
  /** The display name of the log signal defining this kind. */
  name?: string
}

/** How long a kind of log stays in the database, then as archive files; 0 archive days drops it instead. */
export interface Retention {
  kind?: string
  name?: string
  dbDays: number
  archiveDays: number
}

export interface AlertDay {
  day: string
  alerts: number
  today?: boolean
  /** When it leaves the database, as the configuration stands. */
  leaves?: string
  archive?: boolean
}

export interface Storage {
  days: HistoryDay[]
  alertDays: AlertDay[]
  archives: ArchiveFile[]
  archiving: boolean
  dbBytes: number
  /** Freed inside the database file, not yet given back to the disk. */
  freeBytes: number
  archiveBytes: number
  reclaiming: boolean
  disks?: DiskReading[]
  retention?: { default: Retention; kinds: Retention[] | null; alerts?: Retention }
}

export interface Activity {
  day: string
  account: string
  user: string
  minutes: number
  events: number
}

function params(q: SearchQuery): URLSearchParams {
  const p = new URLSearchParams()
  if (q.platform) p.set('platform', q.platform)
  if (q.kind) p.set('kind', q.kind)
  if (q.text) p.set('text', q.text)
  if (q.account) p.set('account', q.account)
  if (q.user) p.set('user', q.user)
  if (q.days) p.set('days', String(q.days))
  if (q.limit) p.set('limit', String(q.limit))
  return p
}

export async function search(q: SearchQuery, signal?: AbortSignal): Promise<SearchResponse> {
  const res = await apiFetch(`/api/search?${params(q).toString()}`, { signal })
  return readJSON<SearchResponse>('search', res)
}

/** The URL the export button points at, so the browser does the download. */
export function searchCsvUrl(q: SearchQuery): string {
  const p = params(q)
  p.set('format', 'csv')
  return `/api/search?${p.toString()}`
}

export async function fetchStorage(signal?: AbortSignal): Promise<Storage> {
  const res = await apiFetch('/api/history', { signal })
  const body = await readJSON<Partial<Storage>>('history', res)
  return {
    days: body.days ?? [],
    alertDays: body.alertDays ?? [],
    archives: body.archives ?? [],
    archiving: body.archiving ?? false,
    dbBytes: body.dbBytes ?? 0,
    freeBytes: body.freeBytes ?? 0,
    archiveBytes: body.archiveBytes ?? 0,
    reclaiming: body.reclaiming ?? false,
    disks: body.disks ?? [],
    retention: body.retention,
  }
}

async function act(what: string, path: string, method: string): Promise<void> {
  const res = await apiFetch(path, { method })
  if (res.status === 409) {
    const body = (await res.json().catch(() => ({}))) as { error?: string }
    throw new Error(body.error ?? `${what} responded 409`)
  }
  await readJSON<unknown>(what, res)
}

/** Moves a past day out of the database into gzip files. */
export function archiveDay(day: string): Promise<void> {
  return act('archive', `/api/history/${encodeURIComponent(day)}/archive`, 'POST')
}

/** Deletes a past day from the database without keeping a file. */
export function deleteDay(day: string): Promise<void> {
  return act('delete', `/api/history/${encodeURIComponent(day)}`, 'DELETE')
}

/** Moves a past day of alerts out of the database into its archive file. */
export function archiveAlertDay(day: string): Promise<void> {
  return act('archive', `/api/alert-days/${encodeURIComponent(day)}/archive`, 'POST')
}

/** Deletes a past day of alerts from the database without keeping a file. */
export function deleteAlertDay(day: string): Promise<void> {
  return act('delete', `/api/alert-days/${encodeURIComponent(day)}`, 'DELETE')
}

export function archiveDownloadUrl(file: string): string {
  return `/api/archives/${encodeURIComponent(file)}`
}

export function deleteArchive(file: string): Promise<void> {
  return act('delete', `/api/archives/${encodeURIComponent(file)}`, 'DELETE')
}

/** Starts giving freed space back to the disk; it carries on in the background. */
export function reclaimSpace(): Promise<void> {
  return act('reclaim', '/api/storage/reclaim', 'POST')
}

export function historyUrl(day: string, format: 'json' | 'csv'): string {
  return `/api/history/${day}?format=${format}`
}

/** Days that have a summary of who was active, newest first; today always. */
export async function fetchActivityDays(signal?: AbortSignal): Promise<string[]> {
  const res = await apiFetch('/api/activity/days', { signal })
  const body = await readJSON<{ days: string[] | null }>('activity days', res)
  return body.days ?? []
}

export async function fetchActivity(day: string, signal?: AbortSignal): Promise<Activity[]> {
  const res = await apiFetch(`/api/activity?day=${encodeURIComponent(day)}`, { signal })
  const body = await readJSON<{ activity: Activity[] | null }>('activity', res)
  return body.activity ?? []
}

export interface BackupFile {
  file: string
  bytes: number
  created: string
}

export async function requestBackup(): Promise<BackupFile> {
  const res = await apiFetch('/api/backup', { method: 'POST' })
  if (res.status === 409) {
    const body = (await res.json().catch(() => ({}))) as { error?: string }
    throw new Error(body.error ?? 'backup responded 409')
  }
  return readJSON<BackupFile>('backup', res)
}

export interface BackupList {
  backups: BackupFile[]
  /** How many backups the server keeps before it refuses a new one. */
  limit: number
}

const DEFAULT_BACKUP_LIMIT = 3

/** The backups kept on the server, newest first. */
export async function fetchBackups(signal?: AbortSignal): Promise<BackupList> {
  const res = await apiFetch('/api/backups', { signal })
  const body = await readJSON<{ backups: BackupFile[] | null; limit?: number }>('backups', res)
  return { backups: body.backups ?? [], limit: body.limit ?? DEFAULT_BACKUP_LIMIT }
}

/** Where the browser fetches a copy from; the server keeps its own. */
export function backupDownloadUrl(file: string): string {
  return `/api/backups/${encodeURIComponent(file)}`
}

/** Removes one backup from the server for good. */
export async function deleteBackup(file: string): Promise<void> {
  const res = await apiFetch(`/api/backups/${encodeURIComponent(file)}`, { method: 'DELETE' })
  // A 200 is not enough: a server that does not know this route answers with
  // the SPA, and reporting that as a deletion would be a lie.
  await readJSON<unknown>('delete', res)
}
