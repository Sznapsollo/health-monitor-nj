import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import i18n from '../i18n'
import { ThemeModeProvider } from '../theme/ThemeModeProvider'
import { SearchPanel } from './SearchPanel'

const rows = [
  {
    platform: 'test',
    signal: 'sendLogs',
    ts: '2026-09-19T10:00:00Z',
    account: 'acme',
    user: 'anna',
    payload: { to: 'user@example.test', subject: 'Order confirmation' },
  },
  {
    platform: 'test',
    signal: 'dailyLogs',
    ts: '2026-09-19T09:59:00Z',
    account: 'acme',
    user: 'bob',
    url: '/api/work/groups',
    payload: { url: '/api/work/groups', ipAddress: '10.0.0.7', executionTime: 142 },
  },
]

function renderPanel() {
  return render(
    <ThemeModeProvider>
      <SearchPanel platform="test" />
    </ThemeModeProvider>,
  )
}

describe('SearchPanel', () => {
  let urls: string[]

  beforeEach(async () => {
    await i18n.changeLanguage('en')
    globalThis.localStorage?.clear()
    urls = []
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string) => {
        urls.push(String(url))
        return {
          ok: true,
          json: async () => ({
            platform: 'test',
            rows,
            count: rows.length,
            kinds: ['dailyLogs', 'sendLogs'],
          }),
        }
      }),
    )
  })

  it('lists what came back, newest first', async () => {
    renderPanel()
    expect(await screen.findByText(/Order confirmation/)).toBeInTheDocument()
    expect(screen.getByText('2 rows')).toBeInTheDocument()
    expect(screen.getByText('anna')).toBeInTheDocument()
  })

  it('keeps its own columns for each kind of log', async () => {
    renderPanel()
    await screen.findByText(/Order confirmation/)
    await userEvent.click(screen.getByRole('button', { name: 'Columns' }))
    await userEvent.click(within(screen.getByRole('dialog')).getByText('ipAddress'))
    await userEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Close' }))
    await waitFor(() =>
      expect(screen.getByRole('columnheader', { name: 'ipAddress' })).toBeInTheDocument(),
    )

    await userEvent.click(screen.getByLabelText('Kind'))
    await userEvent.click(await screen.findByRole('option', { name: 'sendLogs' }))
    await waitFor(() =>
      expect(screen.queryByRole('columnheader', { name: 'ipAddress' })).not.toBeInTheDocument(),
    )

    await userEvent.click(screen.getByLabelText('Kind'))
    await userEvent.click(await screen.findByRole('option', { name: 'Any kind' }))
    await waitFor(() =>
      expect(screen.getByRole('columnheader', { name: 'ipAddress' })).toBeInTheDocument(),
    )
  })

  it('adds, orders and hides columns, and remembers them', async () => {
    const view = renderPanel()
    await screen.findByText(/Order confirmation/)
    await userEvent.click(screen.getByRole('button', { name: 'Columns' }))
    const dialog = screen.getByRole('dialog')

    await userEvent.click(within(dialog).getByText('ipAddress'))
    await userEvent.type(within(dialog).getByLabelText('Add a field'), 'port{Enter}')
    await userEvent.click(within(dialog).getByRole('checkbox', { name: 'Summary' }))
    await userEvent.click(within(dialog).getByRole('button', { name: 'Move ipAddress left' }))
    await userEvent.click(within(dialog).getByRole('button', { name: 'Close' }))

    const headers = () => screen.getAllByRole('columnheader').map((h) => h.textContent)
    await waitFor(() =>
      expect(headers()).toEqual(['#', 'When', 'Kind', 'Account', 'User', 'ipAddress', 'port']),
    )
    expect(screen.getByText('10.0.0.7')).toBeInTheDocument()
    expect(screen.queryByText(/Order confirmation/)).not.toBeInTheDocument()

    view.unmount()
    renderPanel()
    await screen.findByText('10.0.0.7')
    expect(headers()).toEqual(['#', 'When', 'Kind', 'Account', 'User', 'ipAddress', 'port'])
  })

  it('keeps the summary to what describes the row, not its address', async () => {
    renderPanel()
    expect(
      await screen.findByText('url: /api/work/groups · executionTime: 142'),
    ).toBeInTheDocument()
  })

  it('shows the whole row when one is opened', async () => {
    renderPanel()
    const cell = await screen.findByText(/Order confirmation/)
    await userEvent.click(cell)
    expect(await screen.findByText(/"emailType"|"subject"/)).toBeInTheDocument()
  })

  it('asks the server again when the search changes', async () => {
    renderPanel()
    await screen.findByText('2 rows')
    urls.length = 0

    await userEvent.type(screen.getByLabelText('Search'), 'confirmation')
    await waitFor(() => {
      expect(urls.some((u) => u.includes('text=confirmation'))).toBe(true)
    })
  })

  it('waits for typing to pause before asking', async () => {
    renderPanel()
    await screen.findByText('2 rows')
    urls.length = 0

    await userEvent.type(screen.getByLabelText('Search'), 'confirmation')
    await waitFor(() => {
      expect(urls.some((u) => u.includes('text=confirmation'))).toBe(true)
    })
    expect(urls.filter((u) => u.includes('text='))).toHaveLength(1)
  })

  it('drops an older answer that arrives after a newer one', async () => {
    let answerOld: () => void = () => {}
    const signals: AbortSignal[] = []
    vi.stubGlobal(
      'fetch',
      vi.fn((url: string, init?: RequestInit) => {
        if (init?.signal) signals.push(init.signal)
        const body = (subject: string) => ({
          ok: true,
          json: async () => ({
            platform: 'test',
            rows: [{ ...rows[0], payload: { subject } }],
            count: 1,
            kinds: [],
          }),
        })
        if (String(url).includes('text=old')) {
          return new Promise((resolve) => {
            answerOld = () => resolve(body('stale answer'))
          })
        }
        return Promise.resolve(body(String(url).includes('text=new') ? 'fresh answer' : 'first'))
      }),
    )
    renderPanel()
    await screen.findByText(/first/)
    const input = screen.getByLabelText('Search')

    await userEvent.type(input, 'old')
    await waitFor(() => expect(signals).toHaveLength(2))
    await userEvent.clear(input)
    await userEvent.type(input, 'new')
    expect(await screen.findByText(/fresh answer/)).toBeInTheDocument()
    expect(signals[1]!.aborted).toBe(true)

    answerOld()
    await new Promise((r) => setTimeout(r, 0))
    expect(screen.queryByText(/stale answer/)).not.toBeInTheDocument()
    expect(screen.getByText(/fresh answer/)).toBeInTheDocument()
  })

  it('passes the window and the limit to the server', async () => {
    renderPanel()
    await screen.findByText('2 rows')
    urls.length = 0

    await userEvent.click(screen.getByLabelText('How far back'))
    await userEvent.click(await screen.findByRole('option', { name: '7 days' }))

    await waitFor(() => {
      expect(urls.some((u) => u.includes('days=7'))).toBe(true)
    })
  })

  it('renders in Polish', async () => {
    await i18n.changeLanguage('pl')
    renderPanel()
    expect(await screen.findByLabelText('Szukaj')).toBeInTheDocument()
    expect(await screen.findByText('2 wierszy')).toBeInTheDocument()
  })
})
