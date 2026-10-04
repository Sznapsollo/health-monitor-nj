import { describe, expect, it, vi } from 'vitest'

import type { Alert } from '../api/alerts'
import { applyFilters, defaultFilters, useAlertStore } from './useAlertStore'

function alert(over: Partial<Alert> = {}): Alert {
  return {
    id: Math.random().toString(),
    platform: 'test',
    level: 'WARN',
    message: 'something happened',
    count: 1,
    first: '2026-09-19T10:00:00Z',
    last: '2026-09-19T10:00:00Z',
    ...over,
  }
}

describe('applyFilters', () => {
  const alerts = [
    alert({ level: 'ERROR', category: 'job', message: 'job failed', data: { ms: 20 } }),
    alert({ level: 'WARN', category: 'latency', message: '/api/export slow', data: { ms: 2500 } }),
    alert({ level: 'INFO', category: 'status', message: 'web-1 is reporting again' }),
    alert({ level: 'ERROR', category: 'job', message: 'known noise', silenced: true }),
  ]

  it('hides silenced alerts unless asked for', () => {
    expect(applyFilters(alerts, defaultFilters)).toHaveLength(3)
    expect(applyFilters(alerts, { ...defaultFilters, silenced: true })).toHaveLength(4)
  })

  it('filters by level', () => {
    const got = applyFilters(alerts, { ...defaultFilters, levels: ['ERROR'] })
    expect(got).toHaveLength(1)
    expect(got[0].message).toBe('job failed')
  })

  it('filters by category', () => {
    expect(applyFilters(alerts, { ...defaultFilters, categories: ['latency'] })).toHaveLength(1)
  })

  it('filters by any comma-separated term', () => {
    expect(applyFilters(alerts, { ...defaultFilters, text: 'export' })).toHaveLength(1)
    expect(applyFilters(alerts, { ...defaultFilters, text: 'nothing, reporting' })).toHaveLength(1)
    expect(applyFilters(alerts, { ...defaultFilters, text: 'JOB' })).toHaveLength(1)
  })

  it('filters by latency', () => {
    const got = applyFilters(alerts, { ...defaultFilters, minMs: 1000 })
    expect(got).toHaveLength(1)
    expect(got[0].message).toContain('/api/export')
  })

  it('combines filters', () => {
    const got = applyFilters(alerts, {
      ...defaultFilters,
      levels: ['ERROR'],
      categories: ['job'],
      text: 'failed',
    })
    expect(got).toHaveLength(1)
  })
})

describe('loadStatus', () => {
  it('refreshes who is up without touching the alert list', async () => {
    useAlertStore.setState({ platform: 'p', alerts: [{ id: 'kept' } as never], entities: [] })
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => ({
        ok: true,
        json: async () => ({
          entities: [
            {
              platform: 'p',
              signal: 'servers',
              key: 'web-1',
              lastSeen: '2026-09-26T08:00:00Z',
              offline: false,
            },
          ],
          online: 1,
          offline: 0,
        }),
      })),
    )
    await useAlertStore.getState().loadStatus('p')
    expect(useAlertStore.getState().entities).toHaveLength(1)
    expect(useAlertStore.getState().online).toBe(1)
    expect(useAlertStore.getState().alerts).toEqual([{ id: 'kept' }])
    vi.unstubAllGlobals()
  })
})

function deferredFetch() {
  const pending: { url: string; resolve: (body: unknown) => void }[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn(
      (url: string) =>
        new Promise((resolve) => {
          pending.push({
            url: String(url),
            resolve: (body) => resolve({ ok: true, json: async () => body }),
          })
        }),
    ),
  )
  const answer = (match: string, body: unknown) => {
    for (const p of pending.filter((p) => p.url.includes(match))) p.resolve(body)
  }
  return { answer }
}

describe('switching platforms', () => {
  it('drops a slow answer for the platform that was left', async () => {
    useAlertStore.setState({ platform: null, alerts: [], entities: [] })
    const { answer } = deferredFetch()
    const first = useAlertStore.getState().load('a')
    const second = useAlertStore.getState().load('b')

    answer('platform=b', { alerts: [alert({ platform: 'b', message: 'from b' })], entities: [] })
    await second
    answer('platform=a', { alerts: [alert({ platform: 'a', message: 'from a' })], entities: [] })
    await first

    expect(useAlertStore.getState().alerts.map((a) => a.message)).toEqual(['from b'])
    vi.unstubAllGlobals()
  })

  it('drops a status read for the platform that was left', async () => {
    useAlertStore.setState({ platform: 'a', entities: [], online: 0 })
    const { answer } = deferredFetch()
    const status = useAlertStore.getState().loadStatus('a')
    useAlertStore.setState({ platform: 'b' })
    answer('', { entities: [{ key: 'web-1' }], online: 1 })
    await status

    expect(useAlertStore.getState().entities).toEqual([])
    expect(useAlertStore.getState().online).toBe(0)
    vi.unstubAllGlobals()
  })

  it('drops a slow silences answer for the platform that was left', async () => {
    useAlertStore.setState({ silences: [], review: [] })
    const { answer } = deferredFetch()
    const first = useAlertStore.getState().loadSilences('a')
    const second = useAlertStore.getState().loadSilences('b')

    answer('platform=b', { silences: [{ id: 'from-b' }], review: [] })
    await second
    answer('platform=a', { silences: [{ id: 'from-a' }], review: [{ id: 'from-a' }] })
    await first

    expect(useAlertStore.getState().silences.map((s) => s.id)).toEqual(['from-b'])
    expect(useAlertStore.getState().review).toEqual([])
    vi.unstubAllGlobals()
  })

  it('ignores live alerts from another platform', () => {
    useAlertStore.setState({ platform: 'b', historyDay: null, alerts: [] })
    useAlertStore.getState().receive(alert({ platform: 'a', message: 'elsewhere' }), false)
    useAlertStore.getState().receive(alert({ platform: 'b', message: 'here' }), false)
    expect(useAlertStore.getState().alerts.map((a) => a.message)).toEqual(['here'])
  })
})
