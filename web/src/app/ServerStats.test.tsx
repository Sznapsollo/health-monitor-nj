import { render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import i18n from '../i18n'
import { useServerStore } from '../store/useServerStore'
import { ThemeModeProvider } from '../theme/ThemeModeProvider'
import { ServerStats } from './ServerStats'

const resources = {
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
  dbBytes: 10 * 1024 * 1024,
  history: [
    { at: '2026-09-23T11:59:50Z', rssBytes: 80 * 1024 ** 2, cpuPercent: 3 },
    { at: '2026-09-23T12:00:00Z', rssBytes: 83 * 1024 ** 2, cpuPercent: 4 },
  ],
}

const state = {
  intake: { decoded: 1234, kernelDrops: 0 },
  writer: { written: 99, dropped: 0, errors: 0 },
  logWriter: { written: 7, sampled: 0, errors: 0 },
  hotBytes: 2048,
  durableRows: { minute_agg: 500 },
}

describe('ServerStats', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    useServerStore.setState({ resources: null })
  })

  it('shows memory and CPU with their trend, and the counters of intake and writers', async () => {
    await i18n.changeLanguage('en')
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string) => ({
        ok: true,
        json: async () => (url.startsWith('/api/server') ? resources : state),
      })),
    )
    render(
      <ThemeModeProvider>
        <ServerStats />
      </ThemeModeProvider>,
    )

    expect(await screen.findByText('83 MB')).toBeInTheDocument()
    expect(screen.getByText('of a 512 MB limit · Go heap 20 MB allocated')).toBeInTheDocument()
    expect(screen.getByText('3.5 %')).toBeInTheDocument()
    expect(screen.getByRole('img', { name: 'Memory over the last hour' })).toBeInTheDocument()
    expect(await screen.findByText('Writer: logs')).toBeInTheDocument()
    expect(screen.getByText('1,234')).toBeInTheDocument()
    expect(screen.getByText('rows: minute_agg')).toBeInTheDocument()
    expect(screen.getByText('10 MB')).toBeInTheDocument()
  })
})
