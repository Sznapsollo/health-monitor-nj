import { act, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import i18n from '../i18n'
import { useAlertStore } from '../store/useAlertStore'
import { useMonitorStore } from '../store/useMonitorStore'
import { useServerStore } from '../store/useServerStore'
import { ThemeModeProvider } from '../theme/ThemeModeProvider'
import { THEME_STORAGE_KEY } from '../theme/tokens'
import { DisplayPage } from './DisplayPage'

vi.mock('../charts/echarts', () => ({
  init: () => ({ setOption: vi.fn(), resize: vi.fn(), dispose: vi.fn() }),
}))

/** A socket that does nothing: this is about what the screen draws. */
class SilentSocket {
  static readonly OPEN = 1
  readyState = 0
  onopen: (() => void) | null = null
  onclose: (() => void) | null = null
  onerror: (() => void) | null = null
  onmessage: ((e: MessageEvent) => void) | null = null
  send = vi.fn()
  close = vi.fn()
}

const catalogue = {
  platforms: [
    {
      name: 'example',
      signals: [
        {
          name: 'requests',
          kind: 'timeseries',
          displayName: 'Requests',
          dims: [{ name: 'url', displayName: 'URL' }],
          retention: { hotDetailMinutes: 120, hotTotalsMinutes: 2880, durableDays: 30 },
        },
      ],
    },
  ],
}

const dashboards = [
  {
    id: 'ops',
    platform: 'example',
    name: 'Operacje',
    default: true,
    rows: [
      {
        columns: [
          {
            panels: [{ type: 'chart', signal: 'requests' }, { type: 'alerts' }, { type: 'status' }],
          },
        ],
      },
    ],
  },
  { id: 'jobs', platform: 'example', name: 'Kolejki', rows: [{ columns: [{ panels: [] }] }] },
]

const alerts = {
  platform: 'example',
  alerts: [
    {
      id: 'a1',
      platform: 'example',
      level: 'ERROR',
      message: 'mail server unreachable',
      count: 3,
      first: '2026-09-19T08:00:00Z',
      last: '2026-09-19T08:20:00Z',
    },
  ],
  categories: [],
  counts: { ERROR: 1 },
}

const status = {
  platform: 'example',
  entities: [
    {
      platform: 'example',
      signal: 'heartbeat',
      key: 'jobs-1',
      lastSeen: '2026-09-19T08:20:00Z',
      offline: false,
    },
  ],
  online: 1,
  offline: 0,
}

/** The screen's own credential says which arrangement it was paired with. */
function serve(session: Record<string, unknown>, server: Record<string, unknown> = {}) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) => {
      const path = String(url)
      if (path.includes('/api/catalogue')) return { ok: true, json: async () => catalogue }
      if (path.includes('/api/dashboards')) return { ok: true, json: async () => ({ dashboards }) }
      if (path.includes('/api/session')) return { ok: true, json: async () => session }
      if (path.includes('/api/server')) return { ok: true, json: async () => server }
      if (path.includes('/api/alerts')) return { ok: true, json: async () => alerts }
      if (path.includes('/api/status')) return { ok: true, json: async () => status }
      if (path.includes('/api/silences'))
        return { ok: true, json: async () => ({ silences: [], review: [] }) }
      return { ok: true, json: async () => ({}) }
    }),
  )
}

describe('a wall display shows the dashboard its token is paired with', () => {
  beforeEach(async () => {
    await i18n.changeLanguage('en')
    vi.stubGlobal('WebSocket', SilentSocket)
    globalThis.localStorage?.clear()
    useMonitorStore.setState({
      connection: 'closed',
      catalogue: null,
      platform: '',
      platforms: [],
      criteria: {},
      visible: [],
      views: {},
      dashboards: [],
      dashboardId: null,
      pairedDashboard: null,
    })
    useAlertStore.setState({ alerts: [], entities: [], online: 0, offline: 0, error: null })
  })

  it('draws the paired arrangement, and names it', async () => {
    serve({
      required: true,
      authenticated: true,
      readOnly: true,
      kind: 'display',
      platform: 'example',
      dashboard: 'jobs',
    })

    render(
      <ThemeModeProvider>
        <DisplayPage />
      </ThemeModeProvider>,
    )

    // The pairing wins over the platform's own default arrangement.
    expect(await screen.findByText('Kolejki')).toBeInTheDocument()
    expect(useMonitorStore.getState().pairedDashboard).toBe('jobs')
    expect(screen.queryByText('Operacje')).not.toBeInTheDocument()
  })

  it('falls back to the platform default when the screen is not paired', async () => {
    serve({ required: false, authenticated: true, readOnly: false })

    render(
      <ThemeModeProvider>
        <DisplayPage />
      </ThemeModeProvider>,
    )

    // No pairing, so the arrangement the platform opens on, panels and all.
    expect(await screen.findByText('Operacje')).toBeInTheDocument()
    expect(screen.getByText('Alerts')).toBeInTheDocument()
  })

  it('fills the alert and status panels, which nothing else on this page loads', async () => {
    serve({
      required: true,
      authenticated: true,
      readOnly: true,
      kind: 'display',
      platform: 'example',
      dashboard: 'ops',
    })

    render(
      <ThemeModeProvider>
        <DisplayPage />
      </ThemeModeProvider>,
    )

    // An empty alerts panel on the wall was the bug: the screen has no App
    // around it to read them for it.
    expect(await screen.findByText('mail server unreachable')).toBeInTheDocument()
    expect(await screen.findByText('jobs-1')).toBeInTheDocument()
    expect(useAlertStore.getState().error).toBeNull()
  })
})

describe('what a wall display shows around its dashboard', () => {
  const lowDisk = {
    rssBytes: 80 * 1024 ** 2,
    heapBytes: 40 * 1024 ** 2,
    cpuPercent: 3,
    cpuPercent1m: 3.5,
    cores: 4,
    goroutines: 20,
    gcCycles: 1,
    started: '2026-09-26T00:00:00Z',
    uptimeSeconds: 60,
    disks: [
      {
        folders: ['archive', 'database'],
        freeBytes: 8 * 1024 ** 3,
        totalBytes: 600 * 1024 ** 3,
        low: true,
      },
    ],
  }

  beforeEach(async () => {
    await i18n.changeLanguage('en')
    vi.stubGlobal('WebSocket', SilentSocket)
    globalThis.localStorage?.clear()
    useServerStore.setState({ resources: null })
    useMonitorStore.setState({ dashboards: [], pairedDashboard: null, catalogue: null })
  })

  it('shows the system figures and uses the theme its token asks for', async () => {
    serve(
      {
        required: true,
        authenticated: true,
        readOnly: true,
        kind: 'display',
        platform: 'example',
        dashboard: 'ops',
        display: { showSystem: true, theme: 'dark' },
      },
      lowDisk,
    )
    render(
      <ThemeModeProvider>
        <DisplayPage />
      </ThemeModeProvider>,
    )
    expect(await screen.findByText(/CPU 3.5 %/)).toBeInTheDocument()
    expect(
      await screen.findByText('Disk almost full on the monitor: 8.0 GB free of 600.0 GB'),
    ).toBeInTheDocument()
    await vi.waitFor(() => expect(globalThis.localStorage.getItem(THEME_STORAGE_KEY)).toBe('dark'))
  })

  it('warns of a full disk even with the figures off', async () => {
    serve(
      {
        required: true,
        authenticated: true,
        readOnly: true,
        kind: 'display',
        platform: 'example',
        dashboard: 'ops',
        display: {},
      },
      lowDisk,
    )
    render(
      <ThemeModeProvider>
        <DisplayPage />
      </ThemeModeProvider>,
    )
    expect(
      await screen.findByText('Disk almost full on the monitor: 8.0 GB free of 600.0 GB'),
    ).toBeInTheDocument()
    expect(screen.queryByText(/CPU/)).not.toBeInTheDocument()
  })

  it('tells a quiet platform from a lost connection', async () => {
    serve({
      required: true,
      authenticated: true,
      readOnly: true,
      kind: 'display',
      platform: 'example',
      dashboard: 'ops',
      display: { showLive: true },
    })
    render(
      <ThemeModeProvider>
        <DisplayPage />
      </ThemeModeProvider>,
    )
    const tenMinutesAgo = new Date(Date.now() - 10 * 60_000).toISOString()

    act(() =>
      useMonitorStore.setState({
        connection: 'open',
        lastContact: Date.now(),
        serverTime: tenMinutesAgo,
      }),
    )
    expect(await screen.findByText(/^Live · no new data since/)).toBeInTheDocument()

    act(() => useMonitorStore.setState({ serverTime: new Date().toISOString() }))
    expect(await screen.findByText(/^Live · updated/)).toBeInTheDocument()

    act(() =>
      useMonitorStore.setState({ connection: 'closed', lastContact: Date.now() - 3 * 60_000 }),
    )
    expect(await screen.findByText('No connection for 3 min')).toBeInTheDocument()
  })

  it('lets people at the screen pick the theme, starting from automatic', async () => {
    globalThis.localStorage.setItem(THEME_STORAGE_KEY, 'dark')
    serve({
      required: true,
      authenticated: true,
      readOnly: true,
      kind: 'display',
      platform: 'example',
      dashboard: 'ops',
      display: { theme: 'screen' },
    })
    render(
      <ThemeModeProvider>
        <DisplayPage />
      </ThemeModeProvider>,
    )
    const light = await screen.findByRole('button', { name: 'Light' })
    await vi.waitFor(() =>
      expect(globalThis.localStorage.getItem(THEME_STORAGE_KEY)).toBe('system'),
    )
    const wrapper = light.closest('.MuiToggleButtonGroup-root')?.parentElement
    expect(wrapper).toHaveStyle({ opacity: '0' })

    act(() => {
      globalThis.dispatchEvent(new Event('pointermove'))
    })
    await vi.waitFor(() => expect(wrapper).toHaveStyle({ opacity: '1' }))
    await userEvent.click(screen.getByRole('button', { name: 'Dark' }))
    expect(globalThis.localStorage.getItem('hm.displayTheme')).toBe('dark')
    expect(globalThis.localStorage.getItem(THEME_STORAGE_KEY)).toBe('dark')
  })
})
