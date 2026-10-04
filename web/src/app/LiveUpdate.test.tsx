import { render, screen, waitFor } from '@testing-library/react'
import { StrictMode } from 'react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import i18n from '../i18n'
import { useMonitorStore } from '../store/useMonitorStore'
import { ThemeModeProvider } from '../theme/ThemeModeProvider'
import { App } from './App'

// Capture what the chart is actually told to draw.
const setOption = vi.fn()
vi.mock('../charts/echarts', () => ({
  init: () => ({ setOption, resize: vi.fn(), dispose: vi.fn() }),
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
          dims: [{ name: 'url', displayName: 'URL' }],
          retention: { hotDetailMinutes: 120, hotTotalsMinutes: 2880, durableDays: 30 },
        },
      ],
    },
  ],
}

/** A socket the test can push messages through, like the real server does. */
class TestSocket {
  static current: TestSocket | null = null
  readyState = 1
  onopen: (() => void) | null = null
  onclose: (() => void) | null = null
  onerror: (() => void) | null = null
  onmessage: ((e: MessageEvent) => void) | null = null
  sent: string[] = []

  constructor() {
    TestSocket.current = this
    // The browser opens asynchronously; do the same.
    setTimeout(() => this.onopen?.(), 0)
  }
  send(data: string) {
    this.sent.push(data)
  }
  close() {}

  deliver(message: unknown) {
    this.onmessage?.({ data: JSON.stringify(message) } as MessageEvent)
  }
}

function point(minute: number, count: number) {
  return { minute, count, avgMs: 10, minMs: 1, maxMs: 20 }
}

describe('live updates reach the chart', () => {
  beforeEach(async () => {
    await i18n.changeLanguage('en')
    setOption.mockClear()
    globalThis.localStorage?.clear()
    globalThis.localStorage?.setItem('hm.visible', JSON.stringify({ example: ['requests'] }))
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

  it('redraws when an event arrives, without a reload', async () => {
    // StrictMode, because that is what main.tsx does: React mounts, unmounts
    // and mounts again in development, and the socket has to survive it.
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

    socket.deliver({
      t: 'snapshot',
      platforms: ['example'],
      signals: {
        requests: {
          platform: 'example',
          signal: 'requests',
          from: 100,
          to: 110,
          detailFrom: 100,
          total: [point(100, 1)],
        },
      },
    })
    await waitFor(() => {
      expect(useMonitorStore.getState().views.requests?.total).toHaveLength(1)
    })
    setOption.mockClear()

    // A minute changes: the chart has to redraw with the new point, and the
    // viewer must not have to reload the page to see it.
    socket.deliver({
      t: 'event',
      signal: 'requests',
      minute: 101,
      view: {
        platform: 'example',
        signal: 'requests',
        from: 100,
        to: 101,
        detailFrom: 100,
        total: [point(101, 7)],
      },
    })

    await waitFor(() => {
      expect(useMonitorStore.getState().views.requests?.total).toHaveLength(2)
    })
    await waitFor(() => {
      expect(setOption).toHaveBeenCalled()
    })

    const drawn = setOption.mock.calls.at(-1)?.[0] as {
      series: { data: (number | null)[] }[]
    }
    expect(drawn.series[0].data).toEqual([1, 7])
    expect(setOption.mock.calls.at(-1)?.[1]).toEqual({ replaceMerge: ['series', 'yAxis'] })

    // A batching server sends every changed view of a flush in one message.
    socket.deliver({
      t: 'events',
      events: [
        {
          id: 'requests',
          signal: 'requests',
          minute: 102,
          view: {
            platform: 'example',
            signal: 'requests',
            from: 100,
            to: 102,
            detailFrom: 100,
            total: [point(102, 4)],
          },
        },
      ],
    })
    await waitFor(() => {
      expect(useMonitorStore.getState().views.requests?.total).toHaveLength(3)
    })
  })
})
