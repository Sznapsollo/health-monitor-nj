import type { LogRow } from '../api/search'

/** A built-in column, or one showing a field of the row's data ("field:ipAddress"). */
export interface SearchColumn {
  id: string
  visible: boolean
}

export const BUILT_IN = ['when', 'kind', 'account', 'user', 'url', 'level', 'summary'] as const

export const DEFAULT_COLUMNS: SearchColumn[] = [
  { id: 'when', visible: true },
  { id: 'kind', visible: true },
  { id: 'account', visible: true },
  { id: 'user', visible: true },
  { id: 'summary', visible: true },
  { id: 'url', visible: false },
  { id: 'level', visible: false },
]

const KEY = 'hm.search.columns'
const BY_KIND = 'hm.search.columnsByKind'

export const fieldColumn = (name: string) => `field:${name}`
export const fieldOf = (id: string) => (id.startsWith('field:') ? id.slice(6) : null)

function read(key: string): unknown {
  try {
    const raw = globalThis.localStorage?.getItem(key)
    return raw ? (JSON.parse(raw) as unknown) : undefined
  } catch {
    return undefined
  }
}

/**
 * The layout saved for one kind of log ('' for any kind); a kind without one
 * starts from the layout saved before layouts were kept per kind, if any.
 */
export function loadColumns(kind = ''): SearchColumn[] {
  const byKind = read(BY_KIND) as Record<string, unknown> | undefined
  return usable(byKind?.[kind] ?? read(KEY))
}

/** A saved layout, with any built-in column it lacks added at the end, hidden. */
function usable(value: unknown): SearchColumn[] {
  try {
    if (!Array.isArray(value)) return DEFAULT_COLUMNS
    const saved = (value as SearchColumn[]).filter(
      (c) =>
        typeof c?.id === 'string' &&
        (BUILT_IN.includes(c.id as (typeof BUILT_IN)[number]) || fieldOf(c.id)),
    )
    const missing = BUILT_IN.filter((id) => !saved.some((c) => c.id === id)).map((id) => ({
      id,
      visible: false,
    }))
    return saved.length ? [...saved, ...missing] : DEFAULT_COLUMNS
  } catch {
    return DEFAULT_COLUMNS
  }
}

export function saveColumns(kind: string, columns: SearchColumn[]): void {
  try {
    const byKind = (read(BY_KIND) as Record<string, SearchColumn[]> | undefined) ?? {}
    byKind[kind] = columns
    globalThis.localStorage?.setItem(BY_KIND, JSON.stringify(byKind))
  } catch {
    // A browser that keeps nothing still shows the columns for this visit.
  }
}

export function move(columns: SearchColumn[], id: string, by: -1 | 1): SearchColumn[] {
  const i = columns.findIndex((c) => c.id === id)
  const j = i + by
  if (i < 0 || j < 0 || j >= columns.length) return columns
  const out = [...columns]
  ;[out[i], out[j]] = [out[j]!, out[i]!]
  return out
}

/** Fields the rows carry that are not columns yet, most common first. */
export function fieldsFound(rows: LogRow[], columns: SearchColumn[]): string[] {
  const counts = new Map<string, number>()
  for (const row of rows) {
    for (const key of Object.keys(row.payload ?? {})) {
      counts.set(key, (counts.get(key) ?? 0) + 1)
    }
  }
  return [...counts.entries()]
    .filter(([key]) => !columns.some((c) => c.id === fieldColumn(key) || c.id === key))
    .sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]))
    .map(([key]) => key)
}

/** What a field column shows for a row: text as it is, anything else as JSON. */
export function fieldValue(row: LogRow, name: string): string {
  const value = row.payload?.[name]
  if (value === undefined || value === null) return ''
  return typeof value === 'object' ? JSON.stringify(value) : String(value)
}
