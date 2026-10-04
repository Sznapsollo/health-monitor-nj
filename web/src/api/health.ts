import { apiFetch } from './http'

export interface Health {
  status: string
  version: string
  commit: string
  uptimeSeconds: number
  serverTime: string
}

export async function fetchHealth(signal?: AbortSignal): Promise<Health> {
  const res = await apiFetch('/healthz', { signal })
  if (!res.ok) throw new Error(`healthz responded ${res.status}`)
  return (await res.json()) as Health
}
