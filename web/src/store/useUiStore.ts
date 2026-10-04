import { create } from 'zustand'

const KEY = 'hm.ui'

export const MIN_TILE_HEIGHT = 120
export const MAX_TILE_HEIGHT = 520
const HEIGHT_STEP = 60

export interface UiState {
  /** Height of a group tile, the old zoom in / zoom out buttons. */
  tileHeight: number
  /** How many tiles sit side by side on a wide screen. */
  columns: number
  showLegend: boolean
  /** Collapsed tiles, keyed "signal/value". */
  minimised: Record<string, boolean>
  /** Explicit tile order per signal; values not listed keep server order. */
  order: Record<string, string[]>

  setTileHeight: (height: number) => void
  zoom: (direction: 1 | -1) => void
  setColumns: (columns: number) => void
  toggleLegend: () => void
  toggleMinimised: (key: string) => void
  move: (signal: string, value: string, direction: 1 | -1, current: string[]) => void
  reset: () => void
}

const defaults = {
  tileHeight: 180,
  columns: 2,
  showLegend: false,
  minimised: {} as Record<string, boolean>,
  order: {} as Record<string, string[]>,
}

function load(): typeof defaults {
  try {
    const raw = globalThis.localStorage?.getItem(KEY)
    if (!raw) return defaults
    const parsed = JSON.parse(raw) as Partial<typeof defaults>
    return {
      tileHeight: clampHeight(Number(parsed.tileHeight) || defaults.tileHeight),
      columns: Math.min(Math.max(Number(parsed.columns) || defaults.columns, 1), 4),
      showLegend: Boolean(parsed.showLegend),
      minimised: parsed.minimised ?? {},
      order: parsed.order ?? {},
    }
  } catch {
    return defaults
  }
}

function persist(state: UiState): void {
  try {
    const { tileHeight, columns, showLegend, minimised, order } = state
    globalThis.localStorage?.setItem(
      KEY,
      JSON.stringify({ tileHeight, columns, showLegend, minimised, order }),
    )
  } catch {
    // A remembered tile size is not worth an error.
  }
}

function clampHeight(height: number): number {
  return Math.min(Math.max(height, MIN_TILE_HEIGHT), MAX_TILE_HEIGHT)
}

/** Moves one value within an order, returning the new order. */
export function moveWithin(order: string[], value: string, direction: 1 | -1): string[] {
  const from = order.indexOf(value)
  if (from < 0) return order
  const to = from + direction
  if (to < 0 || to >= order.length) return order
  const next = [...order]
  ;[next[from], next[to]] = [next[to], next[from]]
  return next
}

/** Applies a remembered order to what the server sent, keeping newcomers. */
export function applyOrder<T extends { value: string }>(items: T[], order: string[]): T[] {
  if (order.length === 0) return items
  const rank = new Map(order.map((value, i) => [value, i]))
  return [...items].sort((a, b) => {
    const ra = rank.get(a.value)
    const rb = rank.get(b.value)
    if (ra === undefined && rb === undefined) return 0
    // A value the viewer never ordered stays where the server ranked it,
    // after the ones they did.
    if (ra === undefined) return 1
    if (rb === undefined) return -1
    return ra - rb
  })
}

export const useUiStore = create<UiState>((set, get) => ({
  ...load(),

  setTileHeight: (height) => {
    set({ tileHeight: clampHeight(height) })
    persist(get())
  },
  zoom: (direction) => {
    set({ tileHeight: clampHeight(get().tileHeight + direction * HEIGHT_STEP) })
    persist(get())
  },
  setColumns: (columns) => {
    set({ columns: Math.min(Math.max(columns, 1), 4) })
    persist(get())
  },
  toggleLegend: () => {
    set({ showLegend: !get().showLegend })
    persist(get())
  },
  toggleMinimised: (key) => {
    const minimised = { ...get().minimised }
    if (minimised[key]) delete minimised[key]
    else minimised[key] = true
    set({ minimised })
    persist(get())
  },
  move: (signal, value, direction, current) => {
    const existing = get().order[signal] ?? current
    const order = { ...get().order, [signal]: moveWithin(existing, value, direction) }
    set({ order })
    persist(get())
  },
  reset: () => {
    set({ ...defaults })
    persist(get())
  },
}))
