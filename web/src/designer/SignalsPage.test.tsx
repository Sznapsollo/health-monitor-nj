import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import type { Candidate } from '../api/signals'
import i18n from '../i18n'
import { useMonitorStore } from '../store/useMonitorStore'
import { ThemeModeProvider } from '../theme/ThemeModeProvider'
import { SignalsPage } from './SignalsPage'

vi.mock('../charts/echarts', () => ({
  init: () => ({ setOption: vi.fn(), resize: vi.fn(), dispose: vi.fn() }),
}))

const apiCalls: Candidate = {
  platform: 'example',
  signal: 'apiCalls',
  kind: 'metric',
  count: 4,
  first: '2026-09-23T10:00:00Z',
  last: '2026-09-23T10:01:00Z',
  dims: { port: ['8080'], account: ['account-1'] },
  values: { count: { min: 1, max: 1 }, ms: { min: 5, max: 80 } },
  minutes: [{ minute: 100, packets: 4, sums: { count: 4, ms: 120 } }],
}

const shipped = {
  name: 'requests',
  kind: 'timeseries',
  displayName: 'Requests',
  dims: [{ name: 'url', displayName: 'URL' }],
  retention: { hotDetailMinutes: 120, hotTotalsMinutes: 2880, durableDays: 30 },
  readOnly: true,
}

describe('SignalsPage', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('defines a waiting signal from what arrived', async () => {
    await i18n.changeLanguage('en')
    const refreshCatalogue = vi.fn(async () => {})
    useMonitorStore.setState({ refreshCatalogue })
    const calls: { url: string; init?: RequestInit }[] = []
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string, init?: RequestInit) => {
        calls.push({ url, init })
        if (url.startsWith('/api/signals/candidates')) {
          return { ok: true, json: async () => ({ candidates: [apiCalls] }) }
        }
        return { ok: true, json: async () => ({}) }
      }),
    )

    render(
      <ThemeModeProvider>
        <SignalsPage platform="example" specs={[shipped] as never} />
      </ThemeModeProvider>,
    )

    expect(await screen.findByText('apiCalls')).toBeInTheDocument()
    expect(screen.getByText('signals.yaml')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Edit' })).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Define' }))
    expect(await screen.findByText('Preview')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('checkbox', { name: 'Keep account' }))
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(refreshCatalogue).toHaveBeenCalled())
    const put = calls.find((c) => c.init?.method === 'PUT')
    expect(put?.url).toBe('/api/signals/apiCalls?platform=example')
    const body = JSON.parse(String(put?.init?.body)) as Record<string, unknown>
    expect(body).toMatchObject({
      kind: 'timeseries',
      dims: [{ name: 'port', displayName: 'port' }],
      values: { count: 'count', ms: 'ms' },
    })
    expect(await screen.findByText(/apiCalls is defined/)).toBeInTheDocument()
  })

  it('shows the server refusal instead of closing', async () => {
    await i18n.changeLanguage('en')
    vi.stubGlobal(
      'fetch',
      vi.fn(async (_url: string, init?: RequestInit) => {
        if (init?.method === 'PUT') {
          return { ok: false, status: 409, json: async () => ({ error: 'nope, shipped' }) }
        }
        return { ok: true, json: async () => ({ candidates: [apiCalls] }) }
      }),
    )

    render(
      <ThemeModeProvider>
        <SignalsPage platform="example" specs={[]} />
      </ThemeModeProvider>,
    )
    fireEvent.click(await screen.findByRole('button', { name: 'Define' }))
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByText('nope, shipped')).toBeInTheDocument()
  })

  it('defines a log kind with its own days', async () => {
    await i18n.changeLanguage('en')
    useMonitorStore.setState({ refreshCatalogue: vi.fn(async () => {}) })
    const calls: { url: string; init?: RequestInit }[] = []
    const apiSessions: Candidate = {
      platform: 'example',
      signal: 'apiSessionLogs',
      kind: 'log',
      count: 0,
      first: '2026-09-25T00:00:00Z',
      last: '2026-09-25T00:00:00Z',
      dims: {},
      values: {},
      minutes: [],
    }
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string, init?: RequestInit) => {
        calls.push({ url, init })
        if (url.startsWith('/api/signals/candidates')) {
          return { ok: true, json: async () => ({ candidates: [apiSessions] }) }
        }
        return { ok: true, json: async () => ({}) }
      }),
    )
    render(
      <ThemeModeProvider>
        <SignalsPage platform="example" specs={[]} />
      </ThemeModeProvider>,
    )
    fireEvent.click(await screen.findByRole('button', { name: 'Define' }))
    expect(await screen.findByText('How long its days are kept')).toBeInTheDocument()
    expect(screen.queryByText('Preview')).not.toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('Display name'), { target: { value: 'API sessions' } })
    fireEvent.change(screen.getByLabelText('Days in the database'), { target: { value: '5' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(calls.some((c) => c.init?.method === 'PUT')).toBe(true))
    const put = calls.find((c) => c.init?.method === 'PUT')!
    expect(put.url).toBe('/api/signals/apiSessionLogs?platform=example')
    const body = JSON.parse(String(put.init?.body)) as {
      kind: string
      displayName: string
      retention: Record<string, number>
    }
    expect(body.kind).toBe('log')
    expect(body.displayName).toBe('API sessions')
    expect(body.retention.logDays).toBe(5)
    expect(body.retention).not.toHaveProperty('archiveDays')
  })

  it('removes a log sent by mistake after asking, deleting its days', async () => {
    await i18n.changeLanguage('en')
    useMonitorStore.setState({ refreshCatalogue: vi.fn(async () => {}) })
    const typo: Candidate = { ...apiCalls, signal: 'typoLogs', kind: 'log', dims: {}, values: {} }
    const calls: { url: string; method?: string }[] = []
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string, init?: RequestInit) => {
        calls.push({ url, method: init?.method })
        if (url.startsWith('/api/signals/candidates') && !init?.method) {
          return { ok: true, json: async () => ({ candidates: [typo] }) }
        }
        return { ok: true, json: async () => ({}) }
      }),
    )
    const confirm = vi.fn(() => true)
    vi.stubGlobal('confirm', confirm)

    render(
      <ThemeModeProvider>
        <SignalsPage platform="example" specs={[shipped] as never} />
      </ThemeModeProvider>,
    )
    expect(
      await screen.findByText(
        'Stored and searchable already; define it to name it or give it its own days',
      ),
    ).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Remove' }))

    expect(confirm).toHaveBeenCalledWith(expect.stringContaining('delete every stored day of it'))
    await waitFor(() =>
      expect(calls).toContainEqual({
        url: '/api/signals/candidates/typoLogs?platform=example&kind=log',
        method: 'DELETE',
      }),
    )
  })

  it('shows the last packet a waiting signal arrived with', async () => {
    await i18n.changeLanguage('en')
    useMonitorStore.setState({ refreshCatalogue: vi.fn(async () => {}) })
    const report: Candidate = {
      ...apiCalls,
      signal: 'serverMemReport',
      kind: 'info',
      dims: { serverMemName: [] },
      values: {},
    }
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string) => ({
        ok: true,
        json: async () =>
          url.includes('/sample')
            ? { sample: { type: 'serverMemReport', serverMemName: '8080' } }
            : { candidates: [report] },
      })),
    )
    render(
      <ThemeModeProvider>
        <SignalsPage platform="example" specs={[shipped] as never} />
      </ThemeModeProvider>,
    )
    fireEvent.click(await screen.findByRole('button', { name: 'Show packet' }))
    expect(await screen.findByText('Last packet of type serverMemReport')).toBeInTheDocument()
    expect(await screen.findByText('8080')).toBeInTheDocument()
  })

  it('says what an empty log retention falls back to', async () => {
    await i18n.changeLanguage('en')
    useMonitorStore.setState({ refreshCatalogue: vi.fn(async () => {}) })
    const logins: Candidate = {
      ...apiCalls,
      signal: 'loginLogs',
      kind: 'log',
      dims: {},
      values: {},
    }
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string) => ({
        ok: true,
        json: async () =>
          url.startsWith('/api/logs/retention')
            ? { kind: 'loginLogs', dbDays: 1, archiveDays: 2 }
            : { candidates: [logins] },
      })),
    )
    render(
      <ThemeModeProvider>
        <SignalsPage platform="example" specs={[shipped] as never} />
      </ThemeModeProvider>,
    )
    fireEvent.click(await screen.findByRole('button', { name: 'Define' }))
    expect(
      await screen.findByText('Empty: 1 day, as config.yaml says (today and yesterday)'),
    ).toBeInTheDocument()
    expect(
      screen.getByText(
        'Counted from when the file is written; 0 deletes instead of archiving. Empty: 2 days, as config.yaml says',
      ),
    ).toBeInTheDocument()
  })

  it('exports the platform and imports only what it lacks', async () => {
    await i18n.changeLanguage('en')
    const refreshCatalogue = vi.fn(async () => {})
    useMonitorStore.setState({ refreshCatalogue })
    let sent: unknown
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string, init?: RequestInit) => {
        if (url.startsWith('/api/signals/import')) {
          sent = JSON.parse(String(init?.body))
          return {
            ok: true,
            json: async () => ({ imported: ['checkout', 'mails'], skipped: ['requests'] }),
          }
        }
        return { ok: true, json: async () => ({ candidates: [] }) }
      }),
    )

    render(
      <ThemeModeProvider>
        <SignalsPage platform="example" specs={[shipped] as never} />
      </ThemeModeProvider>,
    )

    expect(await screen.findByRole('link', { name: 'Export' })).toHaveAttribute(
      'href',
      '/api/signals/export?platform=example',
    )
    const text = 'signals:\n  checkout:\n    kind: timeseries\n'
    const file = Object.assign(new File([text], 'signals_example.yaml'), {
      text: async () => text,
    })
    fireEvent.change(screen.getByTestId('signals-import'), { target: { files: [file] } })

    expect(
      await screen.findByText(
        'Imported: checkout, mails. Already defined, left as they are: requests.',
      ),
    ).toBeInTheDocument()
    expect(sent).toEqual({ text })
    expect(refreshCatalogue).toHaveBeenCalled()
  })
})
