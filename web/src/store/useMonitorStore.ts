import { create } from 'zustand'

import type { Catalogue, SignalSpec } from '../api/catalogue'
import { criteriaOf, fetchDashboards, type Dashboard } from '../api/dashboards'
import { fetchCatalogue } from '../api/catalogue'
import { fetchSession } from '../api/session'
import {
  criteriaFromSearch,
  loadActive,
  loadActiveView,
  loadDashboardChoice,
  loadSaved,
  loadVisible,
  saveActive,
  saveActiveView,
  saveDashboardChoice,
  saveVisible,
  searchFromCriteria,
  searchFromView,
  viewFromSearch,
} from '../criteria/persistence'
import { HmSocket, type ConnectionState } from '../ws/client'
import type { Criteria, ServerMessage, SignalView } from '../ws/types'
import { useAlertStore } from './useAlertStore'
import { mergeView } from './merge'

export interface Notice {
  level: string
  text: string
  popup?: boolean
  at: number
}

interface MonitorState {
  connection: ConnectionState
  catalogue: Catalogue | null
  catalogueError: string | null
  platforms: string[]
  platform: string
  criteria: Record<string, Criteria>
  /**
   * The charts the Charts tab draws. Only these — plus whatever the open
   * arrangement needs — are asked for, so a chart nobody is looking at is not
   * computed or sent at all.
   */
  visible: string[]
  views: Record<string, SignalView>
  notice: Notice | null
  serverTime: string | null
  /** When anything last arrived from the server, pongs included (ms since epoch). */
  lastContact: number | null
  /** The saved set currently applied, if the filters still match it. */
  activeView: string | null
  /** Arrangements this platform defines, and which one is open. */
  dashboards: Dashboard[]
  dashboardId: string | null
  /**
   * The dashboard a wall display's token is paired with. A screen shows this
   * and nothing else, so what is on the wall is decided where the token is
   * made rather than by whatever URL was last typed into the TV.
   */
  pairedDashboard: string | null
  /** Filters typed on dashboard chart panels, by panel id; this viewer only. */
  panelFilters: Record<string, string>

  connect: () => void
  disconnect: () => void
  loadCatalogue: () => Promise<void>
  /** Re-reads the catalogue after the designer changed it; new signals go on show. */
  refreshCatalogue: () => Promise<void>
  setPlatform: (platform: string) => void
  setCriteria: (criteria: Criteria) => void
  /** Chooses which charts are on show; the rest stop being sent. */
  setVisible: (signals: string[]) => void
  moveVisible: (signal: string, direction: 1 | -1) => void
  /** Applies a whole saved set at once; naming it puts it in the address bar. */
  applyCriteria: (criteria: Criteria[], name?: string | null, visible?: string[]) => void
  /** Back to what each signal's own definition asks for. */
  resetCriteria: () => void
  /** Opens an arrangement, asking the server for the data its charts need. */
  openDashboard: (id: string | null) => void
  setPanelFilter: (id: string, filter: string | null) => void
  /** Re-reads the platform's arrangements after one was saved or deleted. */
  reloadDashboards: (open?: string | null) => Promise<void>
  dismissNotice: () => void
  /** Exposed for tests; the socket owns it in the browser. */
  handleMessage: (msg: ServerMessage) => void
}

/**
 * Whether a set of criteria is simply what the definitions ask for. Used to
 * hide the "clear" control: an action that would change nothing should not be
 * offered.
 */
export function atDefaults(criteria: Criteria[], specs: SignalSpec[]): boolean {
  const timeseries = specs.filter((s) => s.kind === 'timeseries')
  if (criteria.length === 0) return true
  return timeseries.every((spec) => {
    const current = criteria.find((c) => c.signal === spec.name)
    return current === undefined || sameCriteria(current, defaultCriteria(spec))
  })
}

/** Compares what a viewer chose, treating "unset" and "empty" as the same. */
function sameCriteria(a: Criteria, b: Criteria): boolean {
  const text = (v: string | undefined) => v ?? ''
  const num = (v: number | undefined) => v ?? 0
  return (
    a.signal === b.signal &&
    num(a.historyMinutes) === num(b.historyMinutes) &&
    num(a.groupMinutes) === num(b.groupMinutes) &&
    text(a.group) === text(b.group) &&
    num(a.groupTop) === num(b.groupTop) &&
    text(a.groupFilter) === text(b.groupFilter) &&
    text(a.sub) === text(b.sub) &&
    num(a.subTop) === num(b.subTop) &&
    text(a.subFilter) === text(b.subFilter) &&
    text(a.sortBy) === text(b.sortBy)
  )
}

/**
 * The first of the preferred dashboards that still exists, else the default,
 * else the first: a remembered or linked one that was deleted is skipped.
 */
function pickDashboard(
  dashboards: Dashboard[],
  ...preferred: (string | null | undefined)[]
): string | null {
  for (const id of preferred) {
    if (id && dashboards.some((d) => d.id === id)) return id
  }
  return dashboards.find((d) => d.default)?.id ?? dashboards[0]?.id ?? null
}

/** What the Charts menu offers: charts and current values, in catalogue order. */
export function pickable(catalogue: Catalogue | null, platform: string): string[] {
  const signals = catalogue?.platforms.find((p) => p.name === platform)?.signals ?? []
  return signals.filter((s) => s.kind === 'timeseries' || s.kind === 'gauge').map((s) => s.name)
}

/** The criteria a signal starts with, taken from its own definition. */
export function defaultCriteria(spec: SignalSpec): Criteria {
  return {
    signal: spec.name,
    historyMinutes: 60,
    groupMinutes: 120,
    group: '',
    groupTop: spec.views?.find((v) => v.top)?.top ?? 50,
    sortBy: 'count',
  }
}

/**
 * What the server is asked to keep up to date: the charts on show, plus
 * whatever the open arrangement draws. Everything else is left out of the
 * subscription entirely, so the hub neither breaks it down nor sends it.
 */
function wanted(state: MonitorState): Criteria[] {
  const keep = new Set(state.visible)
  const charts = Object.values(state.criteria).filter((c) => keep.has(c.signal))
  const dashboard = state.dashboards.find((d) => d.id === state.dashboardId)
  return dashboard ? [...charts, ...criteriaOf(dashboard, state.panelFilters)] : charts
}

/** Drops the data of views no longer asked for; the rest keeps drawing. */
function keepViews(
  views: Record<string, SignalView>,
  criteria: Criteria[],
): Record<string, SignalView> {
  const keep = new Set(criteria.map((c) => c.id ?? c.signal))
  return Object.fromEntries(Object.entries(views).filter(([id]) => keep.has(id)))
}

let socket: HmSocket | null = null
// React's StrictMode mounts, unmounts and mounts again in development. Tearing
// the socket down in between would close it mid-handshake — harmless, but it
// fills the console with failures that hide real ones. Connections are counted
// instead, and the close is deferred just long enough for a remount to reuse
// the socket that is already opening.
let connections = 0
let closeTimer: ReturnType<typeof setTimeout> | null = null
const CLOSE_DELAY_MS = 250

/** Keeps the address bar in step, so the view can be linked or reloaded. */
function updateUrl(platform: string, criteria: Criteria): void {
  replaceSearch(searchFromCriteria(criteria, platform))
}

function replaceSearch(search: string): void {
  const history = globalThis.history
  if (!history?.replaceState || !globalThis.location) return
  const path = globalThis.location.pathname
  history.replaceState(null, '', search === '' ? path : `${path}?${search}`)
}

function stripUndefined(criteria: Criteria): Partial<Criteria> {
  return Object.fromEntries(
    Object.entries(criteria).filter(([, value]) => value !== undefined),
  ) as Partial<Criteria>
}

// This module owns the WebSocket and the store behind it. Hot-replacing it
// would leave the old socket feeding a store the components no longer read —
// the charts then freeze until the page is reloaded by hand, which looks
// exactly like a broken live update. Take the whole page instead.
if (import.meta.hot) {
  import.meta.hot.invalidate()
}

/**
 * This tab's id for the viewing history. Session storage survives a reload
 * but not the tab, which is exactly one visit.
 */
function tabId(): string {
  try {
    const have = globalThis.sessionStorage?.getItem('hm.tab')
    if (have) return have
    const made = globalThis.crypto?.randomUUID?.() ?? `${Date.now()}-${Math.random()}`
    globalThis.sessionStorage?.setItem('hm.tab', made)
    return made
  } catch {
    return ''
  }
}

export const useMonitorStore = create<MonitorState>((set, get) => ({
  connection: 'closed',
  catalogue: null,
  catalogueError: null,
  platforms: [],
  platform: '',
  criteria: {},
  visible: [],
  views: {},
  notice: null,
  serverTime: null,
  lastContact: null,
  activeView: null,
  dashboards: [],
  dashboardId: null,
  pairedDashboard: null,
  panelFilters: {},

  connect: () => {
    connections += 1
    if (closeTimer !== null) {
      globalThis.clearTimeout(closeTimer)
      closeTimer = null
    }
    if (socket) return

    socket = new HmSocket({
      onMessage: (msg) => get().handleMessage(msg),
      onState: (connection) =>
        set(connection === 'open' ? { connection, lastContact: Date.now() } : { connection }),
    })
    socket.connect()
    socket.send({ t: 'hello', platform: get().platform, criteria: wanted(get()), tab: tabId() })
  },

  disconnect: () => {
    connections = Math.max(0, connections - 1)
    if (connections > 0 || closeTimer !== null) return
    closeTimer = globalThis.setTimeout(() => {
      closeTimer = null
      if (connections > 0) return
      socket?.close()
      socket = null
    }, CLOSE_DELAY_MS)
  },

  loadCatalogue: async () => {
    try {
      const catalogue = await fetchCatalogue()
      const platforms = catalogue.platforms.map((p) => p.name)

      // Precedence, most specific first: a named set in the link, then an
      // explicit link to one chart, then what this browser last looked at,
      // then the signal's own defaults.
      // A wall display is paired with a platform and a dashboard when its
      // token is made; that beats anything in the URL, so a screen cannot be
      // pointed somewhere else by editing its address.
      let paired: { platform?: string; dashboard?: string } = {}
      try {
        const session = await fetchSession()
        if (session.kind === 'display' && session.dashboard) {
          paired = { platform: session.platform, dashboard: session.dashboard }
        }
      } catch {
        // No session layer, or it is unreachable: carry on as a normal viewer.
      }

      const search = globalThis.location?.search ?? ''
      const linkedView = viewFromSearch(search)
      const linked = criteriaFromSearch(search)
      const remembered = loadActive()
      const rememberedView = loadActiveView()
      const platform =
        paired.platform ||
        new URLSearchParams(globalThis.location?.search ?? '').get('platform') ||
        get().platform ||
        (remembered && platforms.includes(remembered.platform) ? remembered.platform : '') ||
        platforms[0] ||
        ''

      const signals = catalogue.platforms.find((p) => p.name === platform)?.signals ?? []
      const criteria: Record<string, Criteria> = {}
      for (const spec of signals) {
        if (spec.kind !== 'timeseries') continue
        const saved =
          remembered?.platform === platform
            ? remembered.criteria.find((c) => c.signal === spec.name)
            : undefined
        criteria[spec.name] = saved ?? defaultCriteria(spec)
      }
      let activeView: string | null = rememberedView
      // Nothing is on show until someone picks it; a remembered choice can
      // only name what this platform still has.
      const names = pickable(catalogue, platform)
      let visible = (loadVisible(platform) ?? []).filter((n) => names.includes(n))

      // A named set in the link replaces everything it mentions.
      if (linkedView) {
        const saved = loadSaved().find((s) => s.name === linkedView)
        if (saved) {
          for (const c of saved.criteria) criteria[c.signal] = c
          if (saved.visible) visible = saved.visible.filter((n) => names.includes(n))
          activeView = linkedView
        } else {
          set({ catalogueError: `no saved filter set called "${linkedView}" in this browser` })
          activeView = null
        }
      } else if (linked && criteria[linked.signal]) {
        criteria[linked.signal] = { ...criteria[linked.signal], ...stripUndefined(linked) }
        // Added, not replacing: the tab writes this link itself on every edit.
        if (!visible.includes(linked.signal)) visible = [...visible, linked.signal]
        // An explicit link describes the filters itself, so it is not a set.
        activeView = null
      }

      set({ catalogue, platforms, platform, criteria, visible, activeView })

      // Arrangements are per platform and shared; a link may name one.
      try {
        const dashboards = await fetchDashboards(platform)
        const openId = pickDashboard(
          dashboards,
          paired.dashboard,
          new URLSearchParams(globalThis.location?.search ?? '').get('dashboard'),
          loadDashboardChoice(platform),
        )
        set({ dashboards, pairedDashboard: paired.dashboard ?? null })
        if (openId) get().openDashboard(openId)
      } catch {
        // A platform without arrangements simply has none.
        set({ dashboards: [] })
      }
      socket?.send({ t: 'hello', platform, criteria: wanted(get()), tab: tabId() })
    } catch (err) {
      set({ catalogueError: err instanceof Error ? err.message : 'unknown error' })
    }
  },

  refreshCatalogue: async () => {
    const catalogue = await fetchCatalogue()
    const { platform, criteria: before, visible: shown } = get()
    if (!platform && catalogue.platforms[0]) {
      set({ catalogue, platforms: catalogue.platforms.map((p) => p.name) })
      get().setPlatform(catalogue.platforms[0].name)
      return
    }
    const signals = catalogue.platforms.find((p) => p.name === platform)?.signals ?? []
    const criteria: Record<string, Criteria> = {}
    for (const spec of signals) {
      if (spec.kind !== 'timeseries') continue
      criteria[spec.name] = before[spec.name] ?? defaultCriteria(spec)
    }
    // A signal defined since is offered in the menu, not put on show.
    const names = pickable(catalogue, platform)
    const visible = shown.filter((n) => names.includes(n))
    set({
      catalogue,
      platforms: catalogue.platforms.map((p) => p.name),
      criteria,
      visible,
      views: keepViews(get().views, wanted({ ...get(), criteria, visible })),
    })
    socket?.send({ t: 'criteria', platform, criteria: wanted(get()) })
  },

  setPlatform: (platform) => {
    const { catalogue } = get()
    const signals = catalogue?.platforms.find((p) => p.name === platform)?.signals ?? []
    const criteria: Record<string, Criteria> = {}
    for (const spec of signals) {
      if (spec.kind === 'timeseries') criteria[spec.name] = defaultCriteria(spec)
    }
    const names = pickable(catalogue, platform)
    const visible = (loadVisible(platform) ?? []).filter((n) => names.includes(n))
    set({ platform, criteria, visible, views: {}, dashboards: [], dashboardId: null })
    saveActive(platform, Object.values(criteria))
    replaceSearch(new URLSearchParams({ platform }).toString())
    socket?.send({ t: 'criteria', platform, criteria: wanted(get()) })
    void fetchDashboards(platform)
      .then((dashboards) => {
        if (get().platform !== platform) return
        set({ dashboards })
        const openId = pickDashboard(dashboards, loadDashboardChoice(platform))
        if (openId) get().openDashboard(openId)
      })
      .catch(() => set({ dashboards: [] }))
  },

  setVisible: (signals) => {
    // The list is the order on the page: what stays keeps its place and what
    // is newly ticked goes last, however the picker hands them over.
    const offered = pickable(get().catalogue, get().platform)
    const kept = get().visible.filter((n) => signals.includes(n) && offered.includes(n))
    const added = offered.filter((n) => signals.includes(n) && !kept.includes(n))
    const visible = [...kept, ...added]
    set({ visible, views: keepViews(get().views, wanted({ ...get(), visible })) })
    saveVisible(get().platform, visible)
    socket?.send({ t: 'criteria', platform: get().platform, criteria: wanted(get()) })
  },

  moveVisible: (signal, direction) => {
    const { catalogue, platform } = get()
    const kindOf = new Map(
      (catalogue?.platforms.find((p) => p.name === platform)?.signals ?? []).map((s) => [
        s.name,
        s.kind,
      ]),
    )
    const visible = [...get().visible]
    const from = visible.indexOf(signal)
    if (from < 0) return
    // Charts and current values are drawn apart, so a step passes over the other kind.
    let to = from + direction
    while (to >= 0 && to < visible.length && kindOf.get(visible[to]) !== kindOf.get(signal)) {
      to += direction
    }
    if (to < 0 || to >= visible.length) return
    ;[visible[from], visible[to]] = [visible[to], visible[from]]
    set({ visible })
    saveVisible(platform, visible)
  },

  applyCriteria: (list, name = null, visible) => {
    const criteria = { ...get().criteria }
    for (const c of list) criteria[c.signal] = c
    const names = pickable(get().catalogue, get().platform)
    set({
      criteria,
      visible: visible ? visible.filter((n) => names.includes(n)) : get().visible,
      views: {},
      activeView: name,
    })
    saveActive(get().platform, Object.values(criteria))
    saveActiveView(name)
    if (visible) saveVisible(get().platform, get().visible)
    // The address bar says what is being looked at: the set's name when one
    // is applied, nothing special when it is an ad-hoc selection.
    if (name) replaceSearch(searchFromView(name, get().platform))
    socket?.send({ t: 'criteria', platform: get().platform, criteria: wanted(get()) })
  },

  openDashboard: (id) => {
    const { dashboards, platform } = get()
    const dashboardId = dashboards.some((d) => d.id === id) ? id : null
    set({ dashboardId })
    if (dashboardId) saveDashboardChoice(platform, dashboardId)
    set({ views: keepViews(get().views, wanted(get())) })
    socket?.send({ t: 'criteria', platform, criteria: wanted(get()) })
  },

  reloadDashboards: async (open) => {
    const { platform } = get()
    const dashboards = await fetchDashboards(platform)
    if (get().platform !== platform) return
    set({ dashboards })
    const wanted = open === undefined ? get().dashboardId : open
    get().openDashboard(pickDashboard(dashboards, wanted, loadDashboardChoice(platform)))
  },

  resetCriteria: () => {
    const { catalogue, platform } = get()
    const signals = catalogue?.platforms.find((p) => p.name === platform)?.signals ?? []
    const criteria: Record<string, Criteria> = {}
    for (const spec of signals) {
      if (spec.kind === 'timeseries') criteria[spec.name] = defaultCriteria(spec)
    }
    // Filters go back to the defaults; what is picked for show stays.
    set({ criteria, views: {}, activeView: null })
    saveActive(platform, Object.values(criteria))
    saveActiveView(null)
    replaceSearch('')
    socket?.send({ t: 'criteria', platform, criteria: wanted(get()) })
  },

  setPanelFilter: (id, filter) => {
    if ((get().panelFilters[id] ?? null) === filter) return
    const panelFilters = { ...get().panelFilters }
    if (filter === null) delete panelFilters[id]
    else panelFilters[id] = filter
    set({ panelFilters })
    socket?.send({ t: 'criteria', platform: get().platform, criteria: wanted(get()) })
  },

  setCriteria: (next) => {
    const criteria = { ...get().criteria, [next.signal]: next }
    // A different grouping means the tiles are rebuilt from the snapshot the
    // server is about to send, so the old ones must not linger.
    const views = { ...get().views }
    delete views[next.signal]
    // Editing a filter means this is no longer the saved set it came from.
    set({ criteria, views, activeView: null })
    saveActive(get().platform, Object.values(criteria))
    saveActiveView(null)
    updateUrl(get().platform, next)
    socket?.send({ t: 'criteria', platform: get().platform, criteria: wanted(get()) })
  },

  dismissNotice: () => set({ notice: null }),

  handleMessage: (msg) => {
    set({ lastContact: Date.now() })
    switch (msg.t) {
      case 'snapshot': {
        const views = { ...msg.signals }
        set({
          views,
          platforms: msg.platforms ?? get().platforms,
          platform: get().platform || msg.platforms?.[0] || '',
          serverTime: msg.serverTime ?? null,
        })
        break
      }
      case 'event': {
        if (!msg.signal || !msg.view) return
        const id = msg.id ?? msg.signal
        const current = get().views[id]
        set({
          views: { ...get().views, [id]: mergeView(current, msg.view) },
          serverTime: msg.serverTime ?? get().serverTime,
        })
        break
      }
      case 'events': {
        if (!msg.events?.length) return
        const views = { ...get().views }
        for (const e of msg.events) {
          views[e.id] = mergeView(views[e.id], e.view)
        }
        set({ views, serverTime: msg.serverTime ?? get().serverTime })
        break
      }
      case 'alert': {
        if (msg.alert) useAlertStore.getState().receive(msg.alert, Boolean(msg.new))
        break
      }
      case 'notice': {
        set({
          notice: {
            level: msg.level ?? 'info',
            text: msg.text ?? '',
            popup: msg.popup,
            at: Date.now(),
          },
        })
        break
      }
      case 'error': {
        console.warn('server refused a message:', msg.text)
        set({ notice: { level: 'error', text: msg.text ?? '', at: Date.now() } })
        break
      }
    }
  },
}))
