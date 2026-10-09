import {
  fetchDashboards,
  saveDashboard,
  slugOf,
  type Column,
  type Dashboard,
  type Row,
} from '../api/dashboards'

const FORMAT = 'hm-dashboards'

/** What a file carries: the arrangement, not who made it or where it lives. */
export interface Portable {
  name: string
  default?: boolean
  rows: Row[]
}

export function portable(d: Dashboard): Portable {
  return { name: d.name, ...(d.default ? { default: true } : {}), rows: structuredClone(d.rows) }
}

/** Saves a copy on another platform, under a new id when its own is taken there. */
export async function copyToPlatform(dashboard: Dashboard, target: string): Promise<Dashboard> {
  const taken = (await fetchDashboards(target)).map((d) => d.id)
  const id = slugOf(dashboard.name, taken)
  return saveDashboard(target, { id, platform: target, ...portable(dashboard) })
}

export function exportText(dashboards: Dashboard[]): string {
  return JSON.stringify(
    { format: FORMAT, version: 1, dashboards: dashboards.map(portable) },
    null,
    2,
  )
}

/**
 * A name safe for any file system: accents dropped to their base letter,
 * anything other than letters, digits, dot and dash replaced by one
 * underscore, and no underscores at either end.
 */
export function fileSafe(name: string): string {
  const safe = name
    .normalize('NFKD')
    .replace(/[\u0300-\u036f]/g, '')
    .replace(/ł/g, 'l')
    .replace(/Ł/g, 'L')
    .replace(/[^A-Za-z0-9.-]+/g, '_')
    .replace(/_+/g, '_')
    .replace(/^[_.]+|[_.]+$/g, '')
  return safe || 'unnamed'
}

export function exportFileName(dashboard: Dashboard): string {
  return `dashboard_${fileSafe(dashboard.name || dashboard.id)}.json`
}

export function exportAllFileName(platform: string): string {
  return `dashboards_${fileSafe(platform)}.json`
}

export function download(filename: string, text: string): void {
  const url = URL.createObjectURL(new Blob([text], { type: 'application/json' }))
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  document.body.appendChild(a)
  a.click()
  a.remove()
  URL.revokeObjectURL(url)
}

/**
 * Reads an exported file: a set of dashboards, a single one, or a bare
 * dashboard as the API returns it. Throws with a readable reason otherwise.
 */
export function parseImport(text: string): Portable[] {
  let data: unknown
  try {
    data = JSON.parse(text)
  } catch {
    throw new Error('not a JSON file')
  }
  const list = isObject(data) && Array.isArray(data.dashboards) ? data.dashboards : [data]
  if (list.length === 0) throw new Error('the file holds no dashboards')
  return list.map((item, i) => {
    if (!isObject(item)) throw new Error(`dashboard ${i + 1} is not an object`)
    const rows = Array.isArray(item.rows)
      ? item.rows
      : Array.isArray(item.columns)
        ? [{ columns: item.columns }]
        : null
    if (!rows || !rows.every(isRow)) throw new Error(`dashboard ${i + 1} has no valid rows`)
    const name = typeof item.name === 'string' && item.name.trim() ? item.name : `Imported ${i + 1}`
    return { name, ...(item.default === true ? { default: true } : {}), rows: rows as Row[] }
  })
}

function isObject(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

function isRow(r: unknown): boolean {
  return isObject(r) && Array.isArray(r.columns) && r.columns.every(isColumn)
}

function isColumn(c: unknown): c is Column {
  return (
    isObject(c) &&
    Array.isArray(c.panels) &&
    c.panels.every((p) => isObject(p) && typeof p.type === 'string')
  )
}
