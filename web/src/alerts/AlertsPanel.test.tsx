import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import i18n from '../i18n'
import { useAlertStore } from '../store/useAlertStore'
import { ThemeModeProvider } from '../theme/ThemeModeProvider'
import type { Alert } from '../api/alerts'
import { AlertsPanel } from './AlertsPanel'

function alert(over: Partial<Alert> = {}): Alert {
  return {
    id: Math.random().toString(),
    platform: 'test',
    level: 'WARN',
    category: 'latency',
    message: '/api/export took 2500 ms',
    count: 1,
    first: '2026-09-19T10:00:00Z',
    last: '2026-09-19T10:00:00Z',
    data: { ms: 2500 },
    ...over,
  }
}

const alerts: Alert[] = [
  alert(),
  alert({ level: 'ERROR', category: 'job', message: 'job failed', data: { ms: 5 } }),
  alert({
    level: 'ERROR',
    category: 'job',
    message: 'known noise',
    silenced: true,
    silenceReason: 'maintenance',
  }),
]

function renderPanel() {
  return render(
    <ThemeModeProvider>
      <AlertsPanel platform="test" />
    </ThemeModeProvider>,
  )
}

describe('AlertsPanel', () => {
  beforeEach(async () => {
    await i18n.changeLanguage('en')
    useAlertStore.setState({
      alerts,
      categories: ['latency', 'job'],
      counts: { ERROR: 1, WARN: 1 },
      silences: [],
      review: [],
      filters: { levels: [], categories: [], text: '', minMs: 0, silenced: false },
      historyDay: null,
      historyDays: [],
      error: null,
    })
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => ({ ok: true, json: async () => ({}) })),
    )
  })

  it('lists the alerts that are not silenced', () => {
    renderPanel()
    expect(screen.getByText('/api/export took 2500 ms')).toBeInTheDocument()
    expect(screen.getByText('job failed')).toBeInTheDocument()
    expect(screen.queryByText('known noise')).not.toBeInTheDocument()
    expect(screen.getByText('Showing 2 of 3')).toBeInTheDocument()
  })

  it('shows silenced alerts with their reason when asked', async () => {
    renderPanel()
    await userEvent.click(screen.getByLabelText('Show silenced'))
    expect(screen.getByText('known noise')).toBeInTheDocument()
    expect(screen.getByText('Silenced: maintenance')).toBeInTheDocument()
  })

  it('filters by level', async () => {
    renderPanel()
    await userEvent.click(screen.getByLabelText('Only ERROR'))
    expect(screen.getByText('job failed')).toBeInTheDocument()
    expect(screen.queryByText('/api/export took 2500 ms')).not.toBeInTheDocument()
  })

  it('filters by text', async () => {
    renderPanel()
    await userEvent.type(screen.getByLabelText('Search'), 'export')
    expect(screen.getByText('/api/export took 2500 ms')).toBeInTheDocument()
    expect(screen.queryByText('job failed')).not.toBeInTheDocument()
  })

  it('filters by latency', async () => {
    renderPanel()
    await userEvent.type(screen.getByLabelText('Slower than (ms)'), '1000')
    expect(screen.getByText('/api/export took 2500 ms')).toBeInTheDocument()
    expect(screen.queryByText('job failed')).not.toBeInTheDocument()
  })

  it('offers to silence a row', async () => {
    renderPanel()
    const buttons = screen.getAllByLabelText('Silence this')
    await userEvent.click(buttons[0])
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    expect(screen.getByLabelText('Text the message contains')).toHaveValue(
      '/api/export took 2500 ms',
    )
    expect(screen.getByRole('button', { name: 'Silence' })).toBeEnabled()
    await userEvent.clear(screen.getByLabelText(/^Reason/))
    expect(screen.getByRole('button', { name: 'Silence' })).toBeDisabled()
  })

  it('renders in Polish', async () => {
    await i18n.changeLanguage('pl')
    renderPanel()
    expect(screen.getByLabelText('Szukaj')).toBeInTheDocument()
    expect(screen.getByText('Widoczne 2 z 3')).toBeInTheDocument()
  })
})

describe('looking back at past alerts', () => {
  beforeEach(async () => {
    await i18n.changeLanguage('en')
    useAlertStore.setState({
      alerts,
      categories: [],
      counts: {},
      silences: [],
      review: [],
      filters: { levels: [], categories: [], text: '', minMs: 0, silenced: false },
      historyDay: null,
      historyDays: ['20260918'],
      error: null,
    })
  })

  it('reads a past day from the history and says so', async () => {
    const fetched: string[] = []
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string) => {
        fetched.push(String(url))
        return {
          ok: true,
          json: async () => ({
            platform: 'test',
            day: '20260918',
            history: true,
            days: ['20260918'],
            categories: [],
            counts: {},
            alerts: [
              {
                id: 'h1',
                platform: 'test',
                level: 'ERROR',
                message: 'import failed',
                count: 12,
                first: '2026-09-18T09:00:00Z',
                last: '2026-09-18T09:00:41Z',
              },
            ],
          }),
        }
      }),
    )

    renderPanel()
    await userEvent.click(screen.getByRole('combobox', { name: 'Day' }))
    await userEvent.click(await screen.findByRole('option', { name: '2026-09-18' }))

    // The recorded burst, with the count the grouping gave it.
    expect(await screen.findByText('import failed')).toBeInTheDocument()
    expect(fetched.some((u) => u.includes('day=20260918'))).toBe(true)
    expect(screen.getByText(/Showing the alerts recorded on 2026-09-18/)).toBeInTheDocument()
    // The live rows are not mixed in with a day that has already gone by.
    expect(screen.queryByText('job failed')).not.toBeInTheDocument()
  })

  it('does not let a live alert land in the middle of a past day', () => {
    useAlertStore.setState({ alerts: [], historyDay: '20260918' })
    useAlertStore.getState().receive(alert({ message: 'happening now' }), true)
    expect(useAlertStore.getState().alerts).toHaveLength(0)
  })
})

describe('paging through many alerts', () => {
  beforeEach(async () => {
    await i18n.changeLanguage('en')
    useAlertStore.setState({
      alerts: Array.from({ length: 120 }, (_, i) => alert({ id: `a${i}`, message: `alert ${i}` })),
      categories: [],
      counts: {},
      silences: [],
      review: [],
      filters: { levels: [], categories: [], text: '', minMs: 0, silenced: false },
      historyDay: null,
      historyDays: [],
      error: null,
    })
  })

  it('shows a page at a time', async () => {
    renderPanel()
    expect(screen.getByText('1–50 of 120')).toBeInTheDocument()
    expect(screen.getByText('alert 0')).toBeInTheDocument()
    expect(screen.queryByText('alert 50')).toBeNull()

    await userEvent.click(screen.getByRole('button', { name: /next page/i }))
    expect(screen.getByText('51–100 of 120')).toBeInTheDocument()
    expect(screen.getByText('alert 50')).toBeInTheDocument()
    expect(screen.queryByText('alert 0')).toBeNull()
  })
})
