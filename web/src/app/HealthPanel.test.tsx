import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import i18n from '../i18n'
import { useHealthStore } from '../store/useHealthStore'
import { useServerStore } from '../store/useServerStore'
import { ThemeModeProvider } from '../theme/ThemeModeProvider'
import { HealthPanel } from './HealthPanel'

const issue = {
  key: 'unknown_signal/demo/checkout/metric',
  source: 'quarantine',
  reason: 'unknown_signal',
  platform: 'demo',
  signal: 'checkout',
  type: 'metric',
  count: 42,
  new: 42,
  last: '2026-09-23T10:00:00Z',
  lastSource: 'shop-1',
  known: false,
}

describe('HealthPanel', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    useHealthStore.setState({ issues: [], unknown: 0, error: null })
    useServerStore.setState({ resources: null })
  })

  it('lists grouped problems with their packet type and marks them known', async () => {
    await i18n.changeLanguage('en')
    const posted: unknown[] = []
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string, init?: RequestInit) => {
        if (url === '/api/health/known') {
          posted.push(JSON.parse(String(init?.body)))
          return {
            ok: true,
            json: async () => ({ issues: [{ ...issue, known: true, new: 0 }], unknown: 0 }),
          }
        }
        if (url.startsWith('/api/server')) {
          return {
            ok: true,
            json: async () => ({
              rssBytes: 83 * 1024 ** 2,
              heapBytes: 20 * 1024 ** 2,
              memLimitBytes: 512 * 1024 ** 2,
              cpuPercent: 4,
              cpuPercent1m: 3.5,
              cores: 2,
              goroutines: 40,
              gcCycles: 12,
              started: '2026-09-23T10:00:00Z',
              uptimeSeconds: 7200,
              history: [
                { at: '2026-09-23T11:59:50Z', rssBytes: 80 * 1024 ** 2, cpuPercent: 3 },
                { at: '2026-09-23T12:00:00Z', rssBytes: 83 * 1024 ** 2, cpuPercent: 4 },
              ],
            }),
          }
        }
        if (url === '/api/health') {
          return { ok: true, json: async () => ({ issues: [issue], unknown: 1 }) }
        }
        return { ok: true, json: async () => ({ intake: {}, writer: {} }) }
      }),
    )

    render(
      <ThemeModeProvider>
        <HealthPanel />
      </ThemeModeProvider>,
    )

    expect(await screen.findByText('Unknown signal')).toBeInTheDocument()
    expect(screen.queryByText('This server')).not.toBeInTheDocument()
    expect(screen.getByText('metric')).toBeInTheDocument()
    expect(screen.getByText('checkout')).toBeInTheDocument()
    expect(screen.getByText('42')).toBeInTheDocument()
    expect(screen.getByText('New')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Mark known' }))
    await waitFor(() => expect(screen.getByText('Known')).toBeInTheDocument())
    expect(posted).toEqual([{ keys: [issue.key], known: true }])
    expect(useHealthStore.getState().unknown).toBe(0)
  })
})
