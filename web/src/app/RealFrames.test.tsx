import { render, screen, waitFor } from '@testing-library/react'
import { StrictMode } from 'react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import i18n from '../i18n'
import { useMonitorStore } from '../store/useMonitorStore'
import { ThemeModeProvider } from '../theme/ThemeModeProvider'
import { App } from './App'

const setOption = vi.fn()
vi.mock('../charts/echarts', () => ({
  init: () => ({ setOption, resize: vi.fn(), dispose: vi.fn() }),
}))

// Captured verbatim from a running server, including the fields my synthetic
// tests never produced: two signals, `detailFrom`, and no `groups` key at all.
const SNAPSHOT = `{"t":"snapshot","serverTime":"2026-09-19T18:13:05.165845016+02:00","platforms":["example"],"signals":{"jobs":{"platform":"example","signal":"jobs","from":29830514,"to":29830573,"total":[{"minute":29830525,"count":2,"avgMs":6.5,"minMs":1,"maxMs":12}],"detailFrom":29830514},"requests":{"platform":"example","signal":"requests","from":29830514,"to":29830573,"total":[{"minute":29830514,"count":4,"avgMs":100,"minMs":10,"maxMs":200}],"detailFrom":29830514}}}`

const EVENTS = [
  `{"t":"event","serverTime":"2026-09-19T18:13:21.747727247+02:00","signal":"requests","minute":29830573,"view":{"platform":"example","signal":"requests","from":29830514,"to":29830573,"total":[{"minute":29830573,"count":1,"avgMs":23,"minMs":23,"maxMs":23}],"detailFrom":29830514}}`,
  `{"t":"event","serverTime":"2026-09-19T18:13:25.748452897+02:00","signal":"requests","minute":29830573,"view":{"platform":"example","signal":"requests","from":29830514,"to":29830573,"total":[{"minute":29830573,"count":3,"avgMs":278.3333333333333,"minMs":23,"maxMs":422}],"detailFrom":29830514}}`,
  `{"t":"event","serverTime":"2026-09-19T18:13:29.748154885+02:00","signal":"requests","minute":29830573,"view":{"platform":"example","signal":"requests","from":29830514,"to":29830573,"total":[{"minute":29830573,"count":6,"avgMs":305.8333333333333,"minMs":23,"maxMs":422}],"detailFrom":29830514}}`,
]

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
        {
          name: 'jobs',
          kind: 'timeseries',
          displayName: 'Jobs',
          dims: [{ name: 'jobName', displayName: 'Job' }],
          retention: { hotDetailMinutes: 120, hotTotalsMinutes: 2880, durableDays: 30 },
        },
      ],
    },
  ],
}

class TestSocket {
  static current: TestSocket | null = null
  readyState = 1
  onopen: (() => void) | null = null
  onclose: (() => void) | null = null
  onerror: (() => void) | null = null
  onmessage: ((e: MessageEvent) => void) | null = null
  constructor() {
    TestSocket.current = this
    setTimeout(() => this.onopen?.(), 0)
  }
  send() {}
  close() {}
  deliverRaw(json: string) {
    this.onmessage?.({ data: json } as MessageEvent)
  }
}

describe('the frames a real server actually sends', () => {
  beforeEach(async () => {
    await i18n.changeLanguage('en')
    setOption.mockClear()
    globalThis.localStorage?.clear()
    globalThis.localStorage?.setItem(
      'hm.visible',
      JSON.stringify({ example: ['requests', 'jobs'] }),
    )
    TestSocket.current = null
    useMonitorStore.setState({
      connection: 'closed',
      catalogue: null,
      catalogueError: null,
      platforms: [],
      platform: '',
      criteria: {},
      views: {},
      notice: null,
      serverTime: null,
    })
    vi.stubGlobal('WebSocket', TestSocket)
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string) => {
        if (String(url).includes('/api/catalogue')) return { ok: true, json: async () => catalogue }
        return { ok: true, json: async () => ({}) }
      }),
    )
  })

  it('keeps redrawing as each event lands', async () => {
    render(
      <StrictMode>
        <ThemeModeProvider>
          <App />
        </ThemeModeProvider>
      </StrictMode>,
    )
    await screen.findByRole('heading', { name: 'Requests' })
    await waitFor(() => expect(TestSocket.current).not.toBeNull())
    const socket = TestSocket.current as TestSocket

    socket.deliverRaw(SNAPSHOT)
    await waitFor(() => expect(useMonitorStore.getState().views.requests).toBeTruthy())

    for (const frame of EVENTS) {
      setOption.mockClear()
      socket.deliverRaw(frame)
      await waitFor(() => expect(setOption).toHaveBeenCalled())
    }

    const total = useMonitorStore.getState().views.requests?.total ?? []
    expect(total.map((p) => [p.minute, p.count])).toEqual([
      [29830514, 4],
      [29830573, 6],
    ])

    const drawn = setOption.mock.calls.at(-1)?.[0] as { series: { data: (number | null)[] }[] }
    expect(drawn.series[0].data).toEqual([4, 6])
  })
})
