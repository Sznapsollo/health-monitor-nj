import { apiFetch } from './http'

/** What the server says exists and how it should be drawn. */
export interface ViewSpec {
  id: string
  type: string
  series?: string
  value?: string
  style?: string
  top?: number
  order?: string[]
  colors?: Record<string, string>
  title?: string
  defaultOn?: boolean
  options?: Record<string, unknown>
}

export interface DimSpec {
  name: string
  displayName: string
  /** Another dimension whose value names this one's, as accountName names account. */
  labelDim?: string
}

export interface SignalSpec {
  name: string
  kind: string
  displayName: string
  dims: DimSpec[]
  views?: ViewSpec[]
  retention: {
    hotDetailMinutes: number
    hotTotalsMinutes: number
    durableDays: number
    detailDays?: number
    logDays?: number
    archiveDays?: number
    versions?: number
  }
  /** The sender's packet type an info signal keeps. */
  packetType?: string
  /** An info signal folding every report of a sender into one. */
  merge?: boolean
  /** An info signal whose senders stay off the Status tab. */
  noStatus?: boolean
  /** Dimension value -> the colour its series is drawn in. */
  colors?: Record<string, string>
  /** Dimension pairs stored always, not only while a chart shows them. */
  keepPairs?: [string, string][]
  autoRegistered?: boolean
  /** Fed by the monitor itself, like the packets received. */
  builtIn?: boolean
  values?: Record<string, string>
  ttlSeconds?: number
  maxKeys?: number
  /** From the platform's signals.yaml: edited there, not in the designer. */
  readOnly?: boolean
  createdBy?: string
  updatedBy?: string
}

export interface PlatformSpec {
  name: string
  signals: SignalSpec[]
}

export interface Catalogue {
  platforms: PlatformSpec[]
}

export async function fetchCatalogue(signal?: AbortSignal): Promise<Catalogue> {
  const res = await apiFetch('/api/catalogue', { signal })
  if (!res.ok) throw new Error(`catalogue responded ${res.status}`)
  const catalogue = (await res.json()) as Catalogue
  for (const platform of catalogue.platforms ?? []) {
    platform.signals = (platform.signals ?? []).map((s) => ({ ...s, dims: s.dims ?? [] }))
  }
  return { platforms: catalogue.platforms ?? [] }
}
