import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

import type { Dashboard } from '../api/dashboards'
import { criteriaOf } from '../api/dashboards'
import i18n from '../i18n'
import { useAlertStore } from '../store/useAlertStore'
import { useMonitorStore } from '../store/useMonitorStore'
import { ThemeModeProvider } from '../theme/ThemeModeProvider'
import { DashboardPage } from './DashboardPage'
import { columnTemplate } from './layout'

const { setOption } = vi.hoisted(() => ({ setOption: vi.fn() }))

vi.mock('../charts/echarts', () => ({
  init: () => ({ setOption, resize: vi.fn(), dispose: vi.fn() }),
}))

// The arrangement from the request: queues above requests on the left, alerts
// down the right.
const ops: Dashboard = {
  id: 'ops',
  platform: 'example',
  name: 'Operacje',
  default: true,
  rows: [
    {
      columns: [
        {
          width: 2,
          panels: [
            { type: 'gauge', signal: 'jobQueuesLoad', height: 200 },
            { type: 'chart', signal: 'requests', group: 'url', top: 5 },
          ],
        },
        { width: 1, panels: [{ type: 'alerts', levels: ['ERROR', 'WARN'], limit: 20 }] },
      ],
    },
  ],
}

const specs = [
  {
    name: 'requests',
    kind: 'timeseries',
    displayName: 'Requests',
    dims: [{ name: 'url', displayName: 'URL' }],
    retention: { hotDetailMinutes: 120, hotTotalsMinutes: 2880, durableDays: 30 },
  },
  {
    name: 'jobQueuesLoad',
    kind: 'gauge',
    displayName: 'Zapełnienie kolejek',
    dims: [],
    retention: { hotDetailMinutes: 120, hotTotalsMinutes: 2880, durableDays: 30 },
  },
]

describe('criteriaOf', () => {
  it('asks the server for what the charts on it draw, one entry per panel', () => {
    expect(criteriaOf(ops)).toEqual([
      {
        id: 'panel:ops:0:0:1',
        signal: 'requests',
        historyMinutes: 60,
        groupMinutes: 120,
        group: 'url',
        sub: '',
        groupFilter: '',
        subFilter: '',
        groupTop: 5,
        sortBy: 'count',
      },
    ])
  })

  it('lets two panels show one signal two ways', () => {
    const twice: Dashboard = {
      ...ops,
      rows: [
        {
          columns: [
            {
              panels: [
                { type: 'chart', signal: 'requests', group: 'url' },
                { type: 'chart', signal: 'requests', group: 'user' },
              ],
            },
          ],
        },
      ],
    }
    const got = criteriaOf(twice)
    expect(got.map((c) => [c.id, c.group])).toEqual([
      ['panel:ops:0:0:0', 'url'],
      ['panel:ops:0:0:1', 'user'],
    ])
  })
})

describe('panel filters', () => {
  const filtered: Dashboard = {
    ...ops,
    rows: [
      {
        columns: [
          {
            panels: [
              { type: 'chart', signal: 'requests', group: 'url', filter: 'orders', subFilter: 'x' },
            ],
          },
        ],
      },
    ],
  }

  it('sends the saved filter, and what a viewer typed instead of it', () => {
    expect(criteriaOf(filtered)[0]).toMatchObject({ groupFilter: 'orders', subFilter: 'x' })
    expect(criteriaOf(filtered, { 'panel:ops:0:0:0': 'cart' })[0]).toMatchObject({
      groupFilter: 'cart',
    })
  })

  it('offers a filter box that overrides the saved filter for this viewer only', async () => {
    await i18n.changeLanguage('en')
    useMonitorStore.setState({ views: {}, panelFilters: {} })
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => ({ ok: true, json: async () => ({}) })),
    )
    render(
      <ThemeModeProvider>
        <DashboardPage dashboard={filtered} platform="example" specs={specs as never} filterable />
      </ThemeModeProvider>,
    )

    const box = screen.getByLabelText('Filter…')
    expect(box).toHaveValue('orders')
    fireEvent.change(box, { target: { value: 'cart' } })
    await vi.waitFor(() =>
      expect(useMonitorStore.getState().panelFilters).toEqual({ 'panel:ops:0:0:0': 'cart' }),
    )
    fireEvent.change(box, { target: { value: 'orders' } })
    await vi.waitFor(() => expect(useMonitorStore.getState().panelFilters).toEqual({}))

    fireEvent.change(box, { target: { value: 'cart' } })
    await vi.waitFor(() =>
      expect(useMonitorStore.getState().panelFilters).toEqual({ 'panel:ops:0:0:0': 'cart' }),
    )
    fireEvent.change(box, { target: { value: '  ' } })
    await vi.waitFor(() => expect(useMonitorStore.getState().panelFilters).toEqual({}))
    fireEvent.blur(box)
    expect(box).toHaveValue('orders')
  })

  it('follows a newly saved filter instead of overriding it', async () => {
    await i18n.changeLanguage('en')
    useMonitorStore.setState({ views: {}, panelFilters: {} })
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => ({ ok: true, json: async () => ({}) })),
    )
    const unfiltered: Dashboard = {
      ...filtered,
      rows: [{ columns: [{ panels: [{ type: 'chart', signal: 'requests', group: 'url' }] }] }],
    }
    const page = (board: Dashboard) => (
      <ThemeModeProvider>
        <DashboardPage dashboard={board} platform="example" specs={specs as never} filterable />
      </ThemeModeProvider>
    )
    const { rerender } = render(page(unfiltered))
    rerender(page(filtered))

    expect(screen.getByLabelText('Filter…')).toHaveValue('orders')
    await new Promise((r) => setTimeout(r, 600))
    expect(useMonitorStore.getState().panelFilters).toEqual({})
  })

  it('leaves the box off a wall display', async () => {
    await i18n.changeLanguage('en')
    render(
      <ThemeModeProvider>
        <DashboardPage dashboard={filtered} platform="example" specs={specs as never} />
      </ThemeModeProvider>,
    )
    expect(screen.queryByLabelText('Filter…')).not.toBeInTheDocument()
  })

  it('says when the main chart counts only what the filter keeps', async () => {
    await i18n.changeLanguage('en')
    useMonitorStore.setState({
      panelFilters: {},
      views: {
        'panel:ops:0:0:0': {
          platform: 'example',
          signal: 'requests',
          from: 100,
          to: 110,
          detailFrom: 100,
          filtered: true,
          total: [{ minute: 110, count: 5, avgMs: 20, minMs: 1, maxMs: 40 }],
        },
      },
    })
    render(
      <ThemeModeProvider>
        <DashboardPage dashboard={filtered} platform="example" specs={specs as never} />
      </ThemeModeProvider>,
    )
    expect(screen.getByText('Only “orders”')).toBeInTheDocument()
  })
})

describe('an alerts panel shows what the file asked for', () => {
  it("applies the panel's own levels and limit", async () => {
    await i18n.changeLanguage('en')
    const alert = (over: Record<string, unknown>) => ({
      id: String(Math.random()),
      platform: 'example',
      level: 'ERROR',
      category: 'jobs',
      message: 'import failed',
      count: 1,
      first: '2026-09-19T10:00:00Z',
      last: '2026-09-19T10:00:00Z',
      ...over,
    })
    useAlertStore.setState({
      alerts: [
        alert({ message: 'first error' }),
        alert({ message: 'second error' }),
        alert({ level: 'INFO', message: 'just so you know' }),
      ],
      filters: { levels: [], categories: [], text: '', minMs: 0, silenced: false },
      historyDay: null,
      historyDays: [],
      error: null,
    } as never)
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => ({ ok: true, json: async () => ({ gauges: [] }) })),
    )

    const board: Dashboard = {
      id: 'ops',
      platform: 'example',
      name: 'Operacje',
      rows: [{ columns: [{ panels: [{ type: 'alerts', levels: ['ERROR'], limit: 1 }] }] }],
    }
    render(
      <ThemeModeProvider>
        <DashboardPage dashboard={board} platform="example" specs={specs as never} />
      </ThemeModeProvider>,
    )

    // One row, of the level the panel asked for; the tab's own filters are
    // not what a written-down arrangement should obey.
    expect(await screen.findByText('first error')).toBeInTheDocument()
    expect(screen.queryByText('second error')).not.toBeInTheDocument()
    expect(screen.queryByText('just so you know')).not.toBeInTheDocument()
  })
})

describe('DashboardPage', () => {
  it('draws each panel where the file puts it', async () => {
    await i18n.changeLanguage('en')
    useMonitorStore.setState({
      views: {
        'panel:ops:0:0:1': {
          platform: 'example',
          signal: 'requests',
          from: 100,
          to: 110,
          detailFrom: 100,
          total: [{ minute: 110, count: 5, avgMs: 20, minMs: 1, maxMs: 40 }],
        },
      },
    })
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => ({
        ok: true,
        json: async () => ({
          gauges: [
            {
              platform: 'example',
              signal: 'jobQueuesLoad',
              sources: ['jobs-1'],
              points: [{ label: 'SEND_MAIL', value: 142 }],
            },
          ],
        }),
      })),
    )

    render(
      <ThemeModeProvider>
        <DashboardPage dashboard={ops} platform="example" specs={specs as never} />
      </ThemeModeProvider>,
    )

    // The gauge's own values, the chart's title, and the alerts panel.
    expect(await screen.findByText('SEND_MAIL')).toBeInTheDocument()
    expect(screen.getByText('142')).toBeInTheDocument()
    expect(screen.getByText('Requests')).toBeInTheDocument()
    expect(screen.getByText('Alerts')).toBeInTheDocument()
  })

  it.each([
    [undefined, 'total'],
    [false, undefined],
  ])('draws sub-groups with stacked=%s as stack %s', async (stackedChoice, stack) => {
    await i18n.changeLanguage('en')
    const stacked: Dashboard = {
      id: 'ports',
      platform: 'example',
      name: 'Ports',
      rows: [
        {
          columns: [
            {
              width: 1,
              panels: [
                {
                  type: 'chart',
                  signal: 'requests',
                  group: 'port',
                  sub: 'account',
                  stacked: stackedChoice,
                  top: 5,
                },
              ],
            },
          ],
        },
      ],
    }
    const point = (count: number) => [{ minute: 110, count, avgMs: 20, minMs: 1, maxMs: 40 }]
    useMonitorStore.setState({
      views: {
        'panel:ports:0:0:0': {
          platform: 'example',
          signal: 'requests',
          from: 100,
          to: 110,
          detailFrom: 100,
          total: point(5),
          groups: [
            {
              value: '8080',
              count: 5,
              avgMs: 20,
              points: point(5),
              groups: [
                { value: 'acme', count: 3, avgMs: 20, points: point(3) },
                { value: 'globex', count: 2, avgMs: 20, points: point(2) },
              ],
            },
          ],
        },
      },
    })
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => ({ ok: true, json: async () => ({}) })),
    )
    setOption.mockClear()

    render(
      <ThemeModeProvider>
        <DashboardPage dashboard={stacked} platform="example" specs={specs as never} />
      </ThemeModeProvider>,
    )

    expect(await screen.findByText('8080')).toBeInTheDocument()
    const drawn = setOption.mock.calls.flatMap(
      ([option]) => (option as { series?: { name: string; stack?: string }[] }).series ?? [],
    )
    const accounts = drawn.filter((s) => s.name === 'acme' || s.name === 'globex')
    expect(accounts.length).toBeGreaterThanOrEqual(2)
    expect(accounts.every((s) => s.stack === stack)).toBe(true)
  })

  it.each([
    [undefined, true],
    [false, false],
  ])('with main=%s draws the main chart: %s', async (mainChoice, drawsMain) => {
    await i18n.changeLanguage('en')
    const byPort: Dashboard = {
      id: 'ports',
      platform: 'example',
      name: 'Ports',
      rows: [
        {
          columns: [
            {
              width: 1,
              panels: [{ type: 'chart', signal: 'requests', group: 'port', main: mainChoice }],
            },
          ],
        },
      ],
    }
    const point = [{ minute: 110, count: 5, avgMs: 20, minMs: 1, maxMs: 40 }]
    useMonitorStore.setState({
      views: {
        'panel:ports:0:0:0': {
          platform: 'example',
          signal: 'requests',
          from: 100,
          to: 110,
          detailFrom: 100,
          total: point,
          groups: [{ value: '8080', count: 5, avgMs: 20, points: point }],
        },
      },
    })
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => ({ ok: true, json: async () => ({}) })),
    )
    setOption.mockClear()

    render(
      <ThemeModeProvider>
        <DashboardPage dashboard={byPort} platform="example" specs={specs as never} />
      </ThemeModeProvider>,
    )

    expect(await screen.findByText('8080')).toBeInTheDocument()
    const drawn = setOption.mock.calls.flatMap(
      ([option]) => (option as { series?: { name: string }[] }).series ?? [],
    )
    expect(drawn.some((s) => s.name === i18n.t('chart.latency'))).toBe(drawsMain)
  })

  it('sends you from a panel to the tab it is a slice of', async () => {
    await i18n.changeLanguage('en')
    const onOpen = vi.fn()

    render(
      <ThemeModeProvider>
        <DashboardPage dashboard={ops} platform="example" specs={specs as never} onOpen={onOpen} />
      </ThemeModeProvider>,
    )

    fireEvent.click(screen.getByLabelText('Open the Alerts tab'))
    expect(onOpen).toHaveBeenCalledWith('alerts')

    // The gauge and the chart both belong to the Charts tab.
    expect(screen.getAllByLabelText('Open the Charts tab')).toHaveLength(2)
  })

  it('turns the relative widths into grid columns', () => {
    // Two thirds and one third, from width: 2 and width: 1.
    expect(columnTemplate(ops.rows[0]!.columns)).toBe('minmax(0, 2fr) minmax(0, 1fr)')
    // A column that does not say counts as one share.
    expect(columnTemplate([{}, { width: 3 }])).toBe('minmax(0, 1fr) minmax(0, 3fr)')
    // And a nonsense width does not collapse the column to nothing.
    expect(columnTemplate([{ width: 0 }, { width: 1 }])).toBe('minmax(0, 1fr) minmax(0, 1fr)')
  })
})
