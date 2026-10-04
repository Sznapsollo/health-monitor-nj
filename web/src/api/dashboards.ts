import type { Criteria } from '../ws/types'
import { apiFetch } from './http'

export type PanelType = 'chart' | 'gauge' | 'alerts' | 'status'

export interface Panel {
  type: PanelType
  title?: string
  signal?: string
  height?: number
  group?: string
  sub?: string
  filter?: string
  subFilter?: string
  stacked?: boolean
  main?: boolean
  together?: boolean
  sort?: string
  top?: number
  minutes?: number
  groupMinutes?: number
  levels?: string[]
  categories?: string[]
  limit?: number
}

export interface Column {
  width?: number
  panels: Panel[]
}

export interface Row {
  columns: Column[]
}

export interface Dashboard {
  id: string
  platform: string
  name: string
  default?: boolean
  rows: Row[]
  createdBy?: string
  updatedBy?: string
  /** From the platform's dashboards.yaml: duplicate it rather than edit it. */
  readOnly?: boolean
}

export async function fetchDashboards(
  platform: string,
  signal?: AbortSignal,
): Promise<Dashboard[]> {
  const res = await apiFetch(`/api/dashboards?platform=${encodeURIComponent(platform)}`, { signal })
  if (!res.ok) throw new Error(`dashboards responded ${res.status}`)
  const body = (await res.json()) as { dashboards: Dashboard[] | null }
  return body.dashboards ?? []
}

export async function saveDashboard(platform: string, dashboard: Dashboard): Promise<Dashboard> {
  const res = await apiFetch(
    `/api/dashboards/${encodeURIComponent(dashboard.id)}?platform=${encodeURIComponent(platform)}`,
    {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(dashboard),
    },
  )
  if (!res.ok) {
    const problem = (await res.json().catch(() => ({}))) as { error?: string }
    throw new Error(problem.error ?? `the server answered ${res.status}`)
  }
  return (await res.json()) as Dashboard
}

export async function deleteDashboard(platform: string, id: string): Promise<void> {
  const res = await apiFetch(
    `/api/dashboards/${encodeURIComponent(id)}?platform=${encodeURIComponent(platform)}`,
    { method: 'DELETE' },
  )
  if (!res.ok) {
    const problem = (await res.json().catch(() => ({}))) as { error?: string }
    throw new Error(problem.error ?? `the server answered ${res.status}`)
  }
}

/** A file-safe id from a name, made unique among the ids already taken. */
export function slugOf(name: string, taken: string[]): string {
  const base =
    name
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, '-')
      .replace(/^-+|-+$/g, '')
      .slice(0, 60) || 'dashboard'
  let id = base
  for (let n = 2; taken.includes(id); n++) id = `${base}-${n}`
  return id
}

/** The id a panel's chart data is keyed by, in the store and on the wire. */
export function panelId(dashboard: Dashboard, row: number, column: number, index: number): string {
  return `panel:${dashboard.id}:${row}:${column}:${index}`
}

/** The criteria a dashboard's charts imply, one per chart panel. */
/** Filters typed on a panel override its saved one; keyed by panel id. */
export function criteriaOf(dashboard: Dashboard, typed: Record<string, string> = {}): Criteria[] {
  const out: Criteria[] = []
  dashboard.rows.forEach((row, r) => {
    row.columns.forEach((column, i) => {
      column.panels.forEach((panel, j) => {
        if (panel.type !== 'chart' || !panel.signal) return
        const id = panelId(dashboard, r, i, j)
        out.push({
          id,
          signal: panel.signal,
          historyMinutes: panel.minutes ?? 60,
          groupMinutes: panel.groupMinutes ?? 120,
          group: panel.group ?? '',
          sub: panel.sub ?? '',
          groupFilter: typed[id] ?? panel.filter ?? '',
          subFilter: panel.subFilter ?? '',
          stacked: panel.stacked,
          main: panel.main,
          together: panel.together,
          groupTop: panel.top ?? 50,
          sortBy: 'count',
        })
      })
    })
  })
  return out
}
