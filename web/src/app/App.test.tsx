import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import i18n from '../i18n'
import { useMonitorStore } from '../store/useMonitorStore'
import { useUiStore } from '../store/useUiStore'
import { ThemeModeProvider } from '../theme/ThemeModeProvider'
import type { ServerMessage } from '../ws/types'
import { App } from './App'

// ECharts draws to a canvas, which jsdom does not implement. The option
// builder is tested on its own in src/charts/options.test.ts.
vi.mock('../charts/echarts', () => ({
  init: () => ({ setOption: vi.fn(), resize: vi.fn(), dispose: vi.fn() }),
}))

const catalogue = {
  platforms: [
    {
      name: 'example',
      signals: [
        {
          name: 'requests',
          kind: 'timeseries',
          displayName: 'Requests',
          dims: [
            { name: 'url', displayName: 'URL' },
            { name: 'user', displayName: 'User' },
          ],
          retention: { hotDetailMinutes: 120, hotTotalsMinutes: 2880, durableDays: 30 },
        },
      ],
    },
  ],
}

/** A socket that never connects, so the page is exercised without one. */
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

function renderApp() {
  return render(
    <ThemeModeProvider>
      <App />
    </ThemeModeProvider>,
  )
}

describe('App', () => {
  beforeEach(() => {
    globalThis.localStorage?.setItem('hm.visible', JSON.stringify({ example: ['requests'] }))
    useMonitorStore.setState({
      connection: 'closed',
      catalogue: null,
      catalogueError: null,
      platforms: [],
      platform: '',
      criteria: {},
      views: {},
      notice: null,
    })
    vi.stubGlobal('WebSocket', SilentSocket)
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string) => {
        if (String(url).includes('/api/catalogue')) {
          return { ok: true, json: async () => catalogue }
        }
        return {
          ok: true,
          json: async () => ({ status: 'ok', version: 'test', commit: 'c', uptimeSeconds: 1 }),
        }
      }),
    )
  })

  afterEach(async () => {
    vi.unstubAllGlobals()
    await i18n.changeLanguage('en')
  })

  it('builds a section per signal from the catalogue', async () => {
    renderApp()
    expect(await screen.findByRole('heading', { name: 'Requests' })).toBeInTheDocument()
    expect(screen.getByText('All')).toBeInTheDocument()
  })

  it('offers the signal’s own dimensions as group-by choices', async () => {
    renderApp()
    await screen.findByRole('heading', { name: 'Requests' })
    // The pickers come from the definition, so a new signal needs no new form.
    expect(screen.getByLabelText('Group by')).toBeInTheDocument()
    expect(screen.getByLabelText('History (main chart)')).toBeInTheDocument()
  })

  it('hides the platform switcher when there is only one platform', async () => {
    renderApp()
    await screen.findByRole('heading', { name: 'Requests' })
    expect(screen.queryByText('example')).not.toBeInTheDocument()
  })

  it('shows the connection state', async () => {
    renderApp()
    await screen.findByRole('heading', { name: 'Requests' })
    useMonitorStore.setState({ connection: 'open' })
    expect(await screen.findByText('Live')).toBeInTheDocument()
  })

  it('renders in Polish', async () => {
    await i18n.changeLanguage('pl')
    renderApp()
    await screen.findByRole('heading', { name: 'Requests' })
    expect(screen.getByText('Wszystko')).toBeInTheDocument()
    expect(screen.getByLabelText('Grupuj wg')).toBeInTheDocument()
  })

  it('reports a catalogue that cannot be loaded', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => {
        throw new Error('connection refused')
      }),
    )
    renderApp()
    expect(await screen.findByText('connection refused')).toBeInTheDocument()
  })

  it('merges a live update into the chart data', async () => {
    renderApp()
    await screen.findByRole('heading', { name: 'Requests' })

    const snapshot: ServerMessage = {
      t: 'snapshot',
      platforms: ['example'],
      signals: {
        requests: {
          platform: 'example',
          signal: 'requests',
          from: 100,
          to: 101,
          detailFrom: 100,
          total: [{ minute: 100, count: 1, avgMs: 10, minMs: 10, maxMs: 10 }],
        },
      },
    }
    useMonitorStore.getState().handleMessage(snapshot)
    useMonitorStore.getState().handleMessage({
      t: 'event',
      signal: 'requests',
      minute: 101,
      view: {
        platform: 'example',
        signal: 'requests',
        from: 100,
        to: 101,
        detailFrom: 100,
        total: [{ minute: 101, count: 5, avgMs: 20, minMs: 20, maxMs: 20 }],
      },
    })

    await waitFor(() => {
      const total = useMonitorStore.getState().views.requests?.total ?? []
      expect(total.map((p) => [p.minute, p.count])).toEqual([
        [100, 1],
        [101, 5],
      ])
    })
  })

  it('shows a notice sent to every viewer', async () => {
    renderApp()
    await screen.findByRole('heading', { name: 'Requests' })
    useMonitorStore
      .getState()
      .handleMessage({ t: 'notice', level: 'info', text: 'back in a minute' })
    expect(await screen.findByText('back in a minute')).toBeInTheDocument()
  })
})

describe('tile controls and saved filters', () => {
  beforeEach(() => {
    globalThis.localStorage?.clear()
    useUiStore.getState().reset()
    globalThis.localStorage?.setItem('hm.visible', JSON.stringify({ example: ['requests'] }))
    useMonitorStore.setState({
      connection: 'closed',
      catalogue: null,
      catalogueError: null,
      platforms: [],
      platform: '',
      criteria: {},
      views: {},
      notice: null,
    })
    vi.stubGlobal('WebSocket', SilentSocket)
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string) => {
        if (String(url).includes('/api/catalogue')) {
          return { ok: true, json: async () => catalogue }
        }
        return { ok: true, json: async () => ({ status: 'ok' }) }
      }),
    )
  })

  it('remembers the tile size and legend across a reload', async () => {
    renderApp()
    await screen.findByRole('heading', { name: 'Requests' })

    const before = useUiStore.getState().tileHeight
    await userEvent.click(screen.getByLabelText('Taller tiles'))
    expect(useUiStore.getState().tileHeight).toBeGreaterThan(before)

    await userEvent.click(screen.getByLabelText('Legend'))
    expect(useUiStore.getState().showLegend).toBe(true)

    // What a reload would read back.
    const stored = JSON.parse(globalThis.localStorage.getItem('hm.ui') ?? '{}')
    expect(stored.showLegend).toBe(true)
    expect(stored.tileHeight).toBe(useUiStore.getState().tileHeight)
  })

  it('saves a named filter set and applies it again', async () => {
    renderApp()
    await screen.findByRole('heading', { name: 'Requests' })

    useMonitorStore.getState().setCriteria({
      signal: 'requests',
      historyMinutes: 180,
      group: 'url',
    })

    await userEvent.type(screen.getByLabelText('Name'), 'slow pages')
    await userEvent.click(screen.getByRole('button', { name: 'Save' }))

    const saved = JSON.parse(globalThis.localStorage.getItem('hm.savedCriteria') ?? '[]')
    expect(saved).toHaveLength(1)
    expect(saved[0].name).toBe('slow pages')
    expect(saved[0].criteria[0].group).toBe('url')
  })

  it('puts the current view in the address bar', async () => {
    const replaceState = vi.fn()
    vi.stubGlobal('history', { replaceState })
    renderApp()
    await screen.findByRole('heading', { name: 'Requests' })

    useMonitorStore.getState().setCriteria({
      signal: 'requests',
      historyMinutes: 180,
      group: 'url',
      sub: 'user',
    })

    expect(replaceState).toHaveBeenCalled()
    const url = String(replaceState.mock.calls.at(-1)?.[2])
    expect(url).toContain('signal=requests')
    expect(url).toContain('group=url')
    expect(url).toContain('sub=user')
    expect(url).toContain('minutes=180')
  })
})
