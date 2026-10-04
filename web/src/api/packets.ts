import { apiFetch, readJSON } from './http'

export interface ReceivedPacket {
  seq: number
  at: string
  from?: string
  port: number
  platform?: string
  sender?: string
  type?: string
  rejected?: string
  size: number
  raw: string
  cut?: boolean
}

export interface PacketsPage {
  entries: ReceivedPacket[]
  seq: number
  keep: number
  rawBytes: number
  windowSeconds: number
}

export async function fetchPackets(since: number): Promise<PacketsPage> {
  return readJSON<PacketsPage>('/api/packets', await apiFetch(`/api/packets?since=${since}`))
}
