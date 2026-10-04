import type { Criteria } from '../ws/types'

export const SAVED_KEY = 'hm.savedCriteria'
export const ACTIVE_KEY = 'hm.criteria'
export const ACTIVE_VIEW_KEY = 'hm.activeView'
export const VISIBLE_KEY = 'hm.visible'
export const DASHBOARD_KEY = 'hm.dashboard'

export interface SavedCriteria {
  name: string
  platform: string
  criteria: Criteria[]
  /**
   * The charts the set shows. Absent in sets saved before selection existed,
   * which means "all of them" — the behaviour those sets were saved with.
   */
  visible?: string[]
}

/**
 * localStorage can throw outright (private windows, blocked site data), and a
 * remembered filter is never worth breaking the page over.
 */
function readStorage(key: string): string | null {
  try {
    return globalThis.localStorage?.getItem(key) ?? null
  } catch {
    return null
  }
}

function writeStorage(key: string, value: string): void {
  try {
    globalThis.localStorage?.setItem(key, value)
  } catch {
    // Nothing to do: the viewer simply starts fresh next time.
  }
}

export function loadSaved(): SavedCriteria[] {
  const raw = readStorage(SAVED_KEY)
  if (!raw) return []
  try {
    const parsed: unknown = JSON.parse(raw)
    if (!Array.isArray(parsed)) return []
    return parsed.filter(isSaved)
  } catch {
    return []
  }
}

export function saveNamed(entry: SavedCriteria): SavedCriteria[] {
  const kept = loadSaved().filter((s) => s.name !== entry.name)
  const next = [...kept, entry].sort((a, b) => a.name.localeCompare(b.name))
  writeStorage(SAVED_KEY, JSON.stringify(next))
  return next
}

export function deleteSaved(name: string): SavedCriteria[] {
  const next = loadSaved().filter((s) => s.name !== name)
  writeStorage(SAVED_KEY, JSON.stringify(next))
  return next
}

/** The criteria in use, so a reload does not reset the panel. */
export function loadActive(): { platform: string; criteria: Criteria[] } | null {
  const raw = readStorage(ACTIVE_KEY)
  if (!raw) return null
  try {
    const parsed = JSON.parse(raw) as { platform?: unknown; criteria?: unknown }
    if (!Array.isArray(parsed.criteria)) return null
    return {
      platform: typeof parsed.platform === 'string' ? parsed.platform : '',
      criteria: parsed.criteria.filter(isCriteria),
    }
  } catch {
    return null
  }
}

export function saveActive(platform: string, criteria: Criteria[]): void {
  writeStorage(ACTIVE_KEY, JSON.stringify({ platform, criteria }))
}

/**
 * Which charts the Charts tab draws, per platform. Only these are asked for,
 * so a hidden chart costs the server nothing.
 */
export function loadVisible(platform: string): string[] | null {
  const raw = readStorage(VISIBLE_KEY)
  if (!raw) return null
  try {
    const parsed = JSON.parse(raw) as Record<string, unknown>
    const list = parsed?.[platform]
    if (!Array.isArray(list)) return null
    return list.filter((v): v is string => typeof v === 'string')
  } catch {
    return null
  }
}

export function saveVisible(platform: string, signals: string[]): void {
  let all: Record<string, string[]> = {}
  const raw = readStorage(VISIBLE_KEY)
  if (raw) {
    try {
      const parsed: unknown = JSON.parse(raw)
      if (parsed && typeof parsed === 'object') all = parsed as Record<string, string[]>
    } catch {
      // A corrupt entry is simply replaced.
    }
  }
  all[platform] = signals
  writeStorage(VISIBLE_KEY, JSON.stringify(all))
}

/** The saved set currently applied, so the picker still shows it after a reload. */
export function loadActiveView(): string | null {
  return readStorage(ACTIVE_VIEW_KEY)
}

export function saveActiveView(name: string | null): void {
  try {
    if (name === null) globalThis.localStorage?.removeItem(ACTIVE_VIEW_KEY)
    else globalThis.localStorage?.setItem(ACTIVE_VIEW_KEY, name)
  } catch {
    // A remembered selection is not worth an error.
  }
}

/**
 * A link can address a whole saved set by name — `?view=slow pages` — which is
 * what a wall display is pointed at. The name is local to the
 * browser that saved it; an explicit link (below) travels anywhere.
 */
export function viewFromSearch(search: string): string | null {
  const name = new URLSearchParams(search).get('view')
  return name && name.trim() !== '' ? name : null
}

/** The query string for a saved set. */
export function searchFromView(name: string, platform?: string): string {
  const params = new URLSearchParams()
  params.set('view', name)
  if (platform) params.set('platform', platform)
  return params.toString()
}

/**
 * Criteria in the query string, so a chart can be linked to. One signal per
 * link keeps the URL readable; everything else falls back to the viewer's own
 * settings.
 */
export function criteriaFromSearch(search: string): Criteria | null {
  const params = new URLSearchParams(search)
  const signal = params.get('signal')
  if (!signal) return null

  const criteria: Criteria = { signal }
  const number = (key: string): number | undefined => {
    const raw = params.get(key)
    if (raw === null) return undefined
    const n = Number(raw)
    return Number.isFinite(n) && n > 0 ? n : undefined
  }
  const text = (key: string): string | undefined => params.get(key) ?? undefined

  criteria.historyMinutes = number('minutes')
  criteria.groupMinutes = number('groupMinutes')
  criteria.group = text('group')
  criteria.groupTop = number('top')
  criteria.groupFilter = text('filter')
  criteria.sub = text('sub')
  criteria.subTop = number('subTop')
  criteria.subFilter = text('subFilter')
  const sort = params.get('sort')
  if (sort === 'count' || sort === 'avgMs') criteria.sortBy = sort

  return criteria
}

/** The query string for a criteria, omitting anything left at its default. */
export function searchFromCriteria(criteria: Criteria, platform?: string): string {
  const params = new URLSearchParams()
  params.set('signal', criteria.signal)
  if (platform) params.set('platform', platform)
  if (criteria.historyMinutes) params.set('minutes', String(criteria.historyMinutes))
  if (criteria.groupMinutes) params.set('groupMinutes', String(criteria.groupMinutes))
  if (criteria.group) params.set('group', criteria.group)
  if (criteria.groupTop) params.set('top', String(criteria.groupTop))
  if (criteria.groupFilter) params.set('filter', criteria.groupFilter)
  if (criteria.sub) params.set('sub', criteria.sub)
  if (criteria.subTop) params.set('subTop', String(criteria.subTop))
  if (criteria.subFilter) params.set('subFilter', criteria.subFilter)
  if (criteria.sortBy && criteria.sortBy !== 'count') params.set('sort', criteria.sortBy)
  return params.toString()
}

function isCriteria(value: unknown): value is Criteria {
  return (
    typeof value === 'object' && value !== null && typeof (value as Criteria).signal === 'string'
  )
}

function isSaved(value: unknown): value is SavedCriteria {
  if (typeof value !== 'object' || value === null) return false
  const s = value as SavedCriteria
  return typeof s.name === 'string' && Array.isArray(s.criteria)
}

/** The dashboard last opened on each platform, so a reload comes back to it. */
export function loadDashboardChoice(platform: string): string | null {
  const raw = readStorage(DASHBOARD_KEY)
  if (!raw) return null
  try {
    const parsed = JSON.parse(raw) as Record<string, unknown>
    const id = parsed?.[platform]
    return typeof id === 'string' ? id : null
  } catch {
    return null
  }
}

export function saveDashboardChoice(platform: string, id: string): void {
  let all: Record<string, string> = {}
  const raw = readStorage(DASHBOARD_KEY)
  if (raw) {
    try {
      const parsed: unknown = JSON.parse(raw)
      if (parsed && typeof parsed === 'object') all = parsed as Record<string, string>
    } catch {
      // A corrupt entry is simply replaced.
    }
  }
  all[platform] = id
  writeStorage(DASHBOARD_KEY, JSON.stringify(all))
}
