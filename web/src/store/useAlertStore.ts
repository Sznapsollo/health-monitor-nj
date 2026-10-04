import { create } from 'zustand'

import {
  fetchAlerts,
  fetchSilences,
  fetchStatus,
  type Alert,
  type Level,
  type Silence,
  type StatusEntity,
} from '../api/alerts'
import {
  loadSettings,
  playChime,
  saveSettings,
  shouldNotify,
  showNotification,
  type NotificationSettings,
} from '../alerts/notifications'

/** At most this many alerts load, live or for a past day: what the server keeps in memory. */
export const ALERTS_LIMIT = 5000

export interface AlertFilters {
  levels: Level[]
  categories: string[]
  text: string
  minMs: number
  silenced: boolean
}

export const defaultFilters: AlertFilters = {
  levels: [],
  categories: [],
  text: '',
  minMs: 0,
  silenced: false,
}

interface AlertState {
  alerts: Alert[]
  categories: string[]
  counts: Partial<Record<Level, number>>
  entities: StatusEntity[]
  online: number
  offline: number
  packets: Record<string, number>
  silences: Silence[]
  review: Silence[]
  filters: AlertFilters
  notifications: NotificationSettings
  error: string | null
  /** A load or a day of history is on its way. */
  loading: boolean
  /**
   * The day being looked at, or null for the live window. History comes from
   * the database and holds one row per burst, which is exactly the shape the
   * live list has, so the panel draws both the same way.
   */
  historyDay: string | null
  /** The days the history holds, newest first. */
  historyDays: string[]
  /** The platform last asked for; answers for any other are dropped. */
  platform: string | null

  load: (platform: string) => Promise<void>
  /** Re-reads only who is up and down, which the heartbeats keep changing. */
  loadStatus: (platform: string) => Promise<void>
  /** Reads one past day instead of the live window. */
  loadHistory: (platform: string, day: string) => Promise<void>
  loadSilences: (platform: string) => Promise<void>
  setFilters: (filters: Partial<AlertFilters>) => void
  setNotifications: (settings: NotificationSettings) => void
  /** Called for every alert arriving over the WebSocket. */
  receive: (alert: Alert, isNew: boolean) => void
}

/** Applies the filters in the browser, so typing feels instant. */
export function applyFilters(alerts: Alert[], f: AlertFilters): Alert[] {
  return alerts.filter((a) => {
    if (a.silenced && !f.silenced) return false
    if (f.levels.length > 0 && !f.levels.includes(a.level)) return false
    if (f.categories.length > 0 && !f.categories.includes(a.category ?? '')) return false
    if (f.minMs > 0 && latencyOf(a) < f.minMs) return false
    if (f.text.trim() !== '') {
      const haystack = `${a.message} ${a.category ?? ''} ${a.source ?? ''}`.toLowerCase()
      const terms = f.text
        .split(',')
        .map((t) => t.trim().toLowerCase())
        .filter(Boolean)
      if (!terms.some((t) => haystack.includes(t))) return false
    }
    return true
  })
}

function latencyOf(a: Alert): number {
  for (const key of ['ms', 'executionTime', 'latencyMs']) {
    const value = a.data?.[key]
    if (typeof value === 'number') return value
  }
  return 0
}

/** Newest first, and an updated alert moves back to the top. */
function merge(current: Alert[], incoming: Alert): Alert[] {
  const without = current.filter((a) => a.id !== incoming.id)
  return [incoming, ...without].slice(0, 2000)
}

// Same reasoning as useMonitorStore: a hot-replaced store is a store the
// components have stopped reading.
if (import.meta.hot) {
  import.meta.hot.invalidate()
}

let loadSeq = 0
let silencesSeq = 0

export const useAlertStore = create<AlertState>((set, get) => ({
  alerts: [],
  categories: [],
  counts: {},
  entities: [],
  online: 0,
  offline: 0,
  packets: {},
  silences: [],
  review: [],
  historyDay: null,
  historyDays: [],
  platform: null,
  filters: defaultFilters,
  notifications: loadSettings(),
  error: null,
  loading: false,

  load: async (platform) => {
    const seq = ++loadSeq
    set({ platform, historyDay: null, loading: true })
    try {
      const [alerts, status] = await Promise.all([
        fetchAlerts({ platform, silenced: true, limit: ALERTS_LIMIT }),
        fetchStatus(platform),
      ])
      if (seq !== loadSeq) return
      // Every field is defaulted: a response that is not the shape we expect
      // must not blank the panel.
      set({
        alerts: alerts.alerts ?? [],
        categories: alerts.categories ?? [],
        counts: alerts.counts ?? {},
        entities: status.entities ?? [],
        online: status.online ?? 0,
        offline: status.offline ?? 0,
        packets: status.packets ?? {},
        error: null,
        loading: false,
      })
    } catch (err) {
      if (seq !== loadSeq) return
      set({ error: err instanceof Error ? err.message : 'unknown error', loading: false })
    }
  },

  loadStatus: async (platform) => {
    try {
      const status = await fetchStatus(platform)
      if (get().platform !== platform) return
      set({
        entities: status.entities ?? [],
        online: status.online ?? 0,
        offline: status.offline ?? 0,
        packets: status.packets ?? {},
      })
    } catch {
      // The next read tries again; the table keeps what it had.
    }
  },

  loadHistory: async (platform, day) => {
    const seq = ++loadSeq
    set({ loading: true })
    try {
      const res = await fetchAlerts({ platform, day, silenced: true, limit: ALERTS_LIMIT })
      if (seq !== loadSeq) return
      set({
        alerts: res.alerts ?? [],
        categories: res.categories ?? [],
        counts: res.counts ?? {},
        historyDay: day,
        historyDays: res.days ?? get().historyDays,
        error: null,
        loading: false,
      })
    } catch (err) {
      if (seq !== loadSeq) return
      set({ error: err instanceof Error ? err.message : 'unknown error', loading: false })
    }
  },

  loadSilences: async (platform) => {
    const seq = ++silencesSeq
    try {
      const res = await fetchSilences(platform)
      if (seq !== silencesSeq) return
      set({ silences: res.silences ?? [], review: res.review ?? [] })
    } catch (err) {
      if (seq !== silencesSeq) return
      set({ error: err instanceof Error ? err.message : 'unknown error' })
    }
  },

  setFilters: (patch) => set({ filters: { ...get().filters, ...patch } }),

  setNotifications: (settings) => {
    saveSettings(settings)
    set({ notifications: settings })
  },

  receive: (alert, isNew) => {
    const { notifications, historyDay, platform } = get()
    if (alert.platform !== platform) return
    // A day that has gone by does not change while it is being looked at; a
    // live alert must not appear in the middle of it.
    if (historyDay === null) set({ alerts: merge(get().alerts, alert) })

    if (!shouldNotify(alert, notifications, isNew)) return
    if (notifications.sound) playChime(alert.level)
    if (notifications.browser) showNotification(alert)
  },
}))
