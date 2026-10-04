import { beforeEach, describe, expect, it, vi } from 'vitest'

import type { Dashboard } from '../api/dashboards'
import type { ClientMessage, Criteria, SignalView } from '../ws/types'
import { useMonitorStore } from './useMonitorStore'

/** A socket that is open from the start and keeps what was sent through it. */
class OpenSocket {
  static readonly OPEN = 1
  static sent: ClientMessage[] = []
  static current: OpenSocket | null = null
  readyState = 1
  onopen: (() => void) | null = null
  onclose: (() => void) | null = null
  onerror: (() => void) | null = null
  onmessage: ((e: MessageEvent) => void) | null = null
  close = vi.fn()

  constructor() {
    OpenSocket.current = this
  }
  send = vi.fn((raw: string) => {
    OpenSocket.sent.push(JSON.parse(raw) as ClientMessage)
  })
}

/** The signals asked for in the last criteria message, in the order sent. */
function lastAsked(): string[] {
  const msg = [...OpenSocket.sent].reverse().find((m) => m.t === 'criteria')
  return (msg?.criteria ?? []).map((c: Criteria) => c.signal)
}

const view = (signal: string): SignalView => ({
  platform: 'example',
  signal,
  from: 100,
  to: 110,
  detailFrom: 100,
  total: [],
})

const spec = (name: string, kind = 'timeseries') => ({
  name,
  kind,
  displayName: name,
  dims: [],
  retention: { hotDetailMinutes: 120, hotTotalsMinutes: 2880, durableDays: 30 },
})

const dashboard: Dashboard = {
  id: 'ops',
  platform: 'example',
  name: 'Operacje',
  rows: [{ columns: [{ panels: [{ type: 'chart', signal: 'jobs' }] }] }],
}

describe('picking which charts are on show', () => {
  beforeEach(() => {
    OpenSocket.sent = []
    vi.stubGlobal('WebSocket', OpenSocket)
    globalThis.localStorage?.clear()
    useMonitorStore.setState({
      platform: 'example',
      catalogue: {
        platforms: [
          { name: 'example', signals: ['requests', 'jobs', 'mails'].map((n) => spec(n)) },
        ],
      },
      criteria: {
        requests: { signal: 'requests' },
        jobs: { signal: 'jobs' },
        mails: { signal: 'mails' },
      },
      visible: ['requests', 'jobs', 'mails'],
      views: { requests: view('requests'), jobs: view('jobs'), mails: view('mails') },
      dashboards: [],
      dashboardId: null,
    })
    useMonitorStore.getState().connect()
  })

  it('asks the server only for the charts on show', () => {
    useMonitorStore.getState().setVisible(['requests'])

    // The hidden ones are left out of the subscription entirely, so the hub
    // neither breaks them down nor sends them.
    expect(lastAsked()).toEqual(['requests'])
    // And their data goes with them, rather than lingering as a stale chart.
    expect(Object.keys(useMonitorStore.getState().views)).toEqual(['requests'])
  })

  it('keeps the order on the page and puts a newly ticked chart last', () => {
    useMonitorStore.setState({ visible: ['mails', 'requests'] })
    useMonitorStore.getState().setVisible(['requests', 'jobs', 'mails'])
    expect(useMonitorStore.getState().visible).toEqual(['mails', 'requests', 'jobs'])
  })

  it('moves a chart up and down, and remembers it', () => {
    useMonitorStore.getState().moveVisible('mails', -1)
    expect(useMonitorStore.getState().visible).toEqual(['requests', 'mails', 'jobs'])
    useMonitorStore.getState().moveVisible('requests', -1)
    expect(useMonitorStore.getState().visible).toEqual(['requests', 'mails', 'jobs'])
    useMonitorStore.getState().moveVisible('requests', 1)
    expect(useMonitorStore.getState().visible).toEqual(['mails', 'requests', 'jobs'])
    expect(globalThis.localStorage?.getItem('hm.visible')).toBe(
      JSON.stringify({ example: ['mails', 'requests', 'jobs'] }),
    )
  })

  it('steps a chart past current values, which are drawn apart', () => {
    useMonitorStore.setState({
      catalogue: {
        platforms: [
          {
            name: 'example',
            signals: [spec('requests'), spec('queues', 'gauge'), spec('jobs')],
          },
        ],
      },
      visible: ['requests', 'queues', 'jobs'],
    })
    useMonitorStore.getState().moveVisible('jobs', -1)
    expect(useMonitorStore.getState().visible).toEqual(['jobs', 'queues', 'requests'])
  })

  it('still asks for what the open arrangement draws, under its own id', () => {
    useMonitorStore.setState({ dashboards: [dashboard], dashboardId: 'ops' })
    useMonitorStore.getState().setVisible(['requests'])

    expect(lastAsked().sort()).toEqual(['jobs', 'requests'])
    const msg = [...OpenSocket.sent].reverse().find((m) => m.t === 'criteria')
    const ids = (msg?.criteria ?? []).map((c: Criteria) => c.id ?? c.signal)
    expect(ids.sort()).toEqual(['panel:ops:0:0:0', 'requests'])
  })

  it("opening an arrangement leaves the tab's own filters alone", () => {
    useMonitorStore.setState({
      criteria: { jobs: { signal: 'jobs', group: 'account', groupTop: 7 } },
      visible: ['jobs'],
      dashboards: [
        {
          ...dashboard,
          rows: [{ columns: [{ panels: [{ type: 'chart', signal: 'jobs', group: 'jobName' }] }] }],
        },
      ],
    })
    useMonitorStore.getState().openDashboard('ops')

    expect(useMonitorStore.getState().criteria.jobs).toEqual({
      signal: 'jobs',
      group: 'account',
      groupTop: 7,
    })
    const msg = [...OpenSocket.sent].reverse().find((m) => m.t === 'criteria')
    const groups = (msg?.criteria ?? []).map((c: Criteria) => [c.id ?? c.signal, c.group])
    expect(groups.sort()).toEqual([
      ['jobs', 'account'],
      ['panel:ops:0:0:0', 'jobName'],
    ])
  })

  it('remembers the choice for next time', () => {
    useMonitorStore.getState().setVisible(['mails'])
    expect(globalThis.localStorage?.getItem('hm.visible')).toContain('mails')
  })

  it('offers a signal defined since in the menu without putting it on show', async () => {
    useMonitorStore.getState().setVisible(['requests'])
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => ({
        ok: true,
        json: async () => ({
          platforms: [
            {
              name: 'example',
              signals: [
                ...['requests', 'jobs', 'mails', 'apiCalls'].map((n) => spec(n)),
                spec('vpnUsage', 'gauge'),
              ],
            },
          ],
        }),
      })),
    )

    await useMonitorStore.getState().refreshCatalogue()

    expect(useMonitorStore.getState().visible).toEqual(['requests'])
    expect(lastAsked()).toEqual(['requests'])
    expect(useMonitorStore.getState().criteria.apiCalls).toBeDefined()
  })

  it('picks current values alongside charts', () => {
    useMonitorStore.setState({
      catalogue: {
        platforms: [
          { name: 'example', signals: [spec('requests'), spec('queues', 'gauge'), spec('jobs')] },
        ],
      },
    })
    useMonitorStore.setState({ visible: [] })
    useMonitorStore.getState().setVisible(['jobs', 'queues'])
    expect(useMonitorStore.getState().visible).toEqual(['queues', 'jobs'])
    expect(lastAsked()).toEqual(['jobs'])
  })
})
