import { apiFetch } from './http'

export interface Issue {
  key: string
  source: 'quarantine' | 'counter'
  reason: string
  platform?: string
  signal?: string
  type?: string
  count: number
  new: number
  first?: string
  last?: string
  lastSource?: string
  known: boolean
  knownBy?: string
  knownAt?: string
}

export interface HealthReport {
  issues: Issue[]
  unknown: number
}

export async function fetchIssues(): Promise<HealthReport> {
  const res = await apiFetch('/api/health')
  if (!res.ok) throw new Error(`/api/health responded ${res.status}`)
  return (await res.json()) as HealthReport
}

export async function markKnown(keys: string[], known: boolean): Promise<HealthReport> {
  const res = await apiFetch('/api/health/known', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ keys, known }),
  })
  if (!res.ok) throw new Error(`/api/health/known responded ${res.status}`)
  return (await res.json()) as HealthReport
}
