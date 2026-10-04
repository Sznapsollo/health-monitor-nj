import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import i18n from '../i18n'
import { ThemeModeProvider } from '../theme/ThemeModeProvider'
import { StoragePanel } from './StoragePanel'

const storage = {
  days: [
    { day: '20260925', rows: 2334, bytes: 700 * 1024, today: true, estimated: true },
    { day: '20260922', rows: 0, bytes: 0, measuring: true },
    {
      day: '20260924',
      rows: 3074145,
      bytes: 1.35 * 1024 ** 3,
      leaves: [
        { kind: 'auditLogs', on: '2026-10-02T00:54:00Z', archive: false },
        { kind: 'dailyLogs', on: '2026-09-27T00:54:00Z', archive: true },
      ],
    },
  ],
  archives: [
    {
      written: '2026-09-25T10:00:00Z',
      deleteOn: '2026-10-25T10:00:00Z',
      file: 'logs-20260923-dailyLogs.ndjson.gz',
      day: '20260923',
      kind: 'dailyLogs',
      bytes: 106 * 1024 ** 2,
    },
    {
      written: '2026-09-25T10:00:00Z',
      file: 'alerts-20260915.ndjson.gz',
      day: '20260915',
      kind: 'alerts',
      alerts: true,
      bytes: 40 * 1024,
    },
  ],
  alertDays: [
    { day: '20260925', alerts: 804, today: true },
    { day: '20260924', alerts: 3370, leaves: '2026-10-05T00:54:00Z', archive: true },
  ],
  archiving: true,
  dbBytes: 1.5 * 1024 ** 3,
  freeBytes: 0,
  archiveBytes: 106 * 1024 ** 2,
  reclaiming: false,
  disks: [
    {
      folders: ['archive', 'database'],
      freeBytes: 61.2 * 1024 ** 3,
      totalBytes: 600 * 1024 ** 3,
      low: false,
    },
  ],
  retention: {
    default: { dbDays: 14, archiveDays: 30 },
    kinds: [
      { kind: 'dailyLogs', dbDays: 2, archiveDays: 30 },
      { kind: 'auditLogs', dbDays: 7, archiveDays: 0 },
    ],
    alerts: { kind: 'alerts', dbDays: 10, archiveDays: 10 },
  },
}

describe('StoragePanel', () => {
  let calls: { url: string; method: string }[]

  beforeEach(async () => {
    await i18n.changeLanguage('en')
    calls = []
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string, init?: RequestInit) => {
        calls.push({ url: String(url), method: init?.method ?? 'GET' })
        const body = String(url).startsWith('/api/backups') ? { backups: [], limit: 3 } : storage
        return { ok: true, status: 200, json: async () => body }
      }),
    )
  })

  afterEach(() => vi.unstubAllGlobals())

  function renderPanel() {
    return render(
      <ThemeModeProvider>
        <StoragePanel />
      </ThemeModeProvider>,
    )
  }

  it('shows the size of every day and archive file, and keeps today out of reach', async () => {
    renderPanel()
    const logs = (await screen.findByText('Days of logs held')).parentElement!
    const today = within(logs).getByText('20260925').closest('tr')!
    expect(within(today).getByText('≈ 700 KB')).toBeInTheDocument()
    expect(within(today).getByText('Today, still being written')).toBeInTheDocument()
    expect(within(today).queryByRole('button', { name: 'Delete day' })).toBeNull()

    const yesterday = within(logs).getByText('20260924').closest('tr')!
    expect(within(yesterday).getByText('1.35 GB')).toBeInTheDocument()
    expect(within(yesterday).getByRole('button', { name: /Archive/ })).toBeInTheDocument()

    const file = screen.getByText('20260923').closest('tr')!
    expect(within(file).getByText('106.0 MB')).toBeInTheDocument()
    expect(within(file).getByRole('link', { name: /gzip/ })).toHaveAttribute(
      'href',
      '/api/archives/logs-20260923-dailyLogs.ndjson.gz',
    )
  })

  it('deletes a day only once its date is typed', async () => {
    renderPanel()
    const logs = (await screen.findByText('Days of logs held')).parentElement!
    const yesterday = within(logs).getByText('20260924').closest('tr')!
    await userEvent.click(within(yesterday).getByRole('button', { name: 'Delete day' }))

    const dialog = await screen.findByRole('dialog')
    const confirm = within(dialog).getByRole('button', { name: 'Delete day' })
    expect(confirm).toBeDisabled()
    await userEvent.type(within(dialog).getByLabelText('Day to delete'), '20260924')
    await userEvent.click(confirm)

    await waitFor(() =>
      expect(calls).toContainEqual({ url: '/api/history/20260924', method: 'DELETE' }),
    )
  })

  it('says how long each kind is kept, from the configuration', async () => {
    renderPanel()
    expect(
      await screen.findByText(
        'dailyLogs: kept in the database for 2 days (searchable), then as gzip files, each deleted 30 days after it is written.',
      ),
    ).toBeInTheDocument()
    expect(
      screen.getByText('auditLogs: kept in the database for 7 days, then deleted.'),
    ).toBeInTheDocument()
    expect(
      screen.getByText(/^Any other kind: kept in the database for 14 days/),
    ).toBeInTheDocument()
  })

  it('lists alert archives with when each file will be deleted', async () => {
    renderPanel()
    const logs = (await screen.findByText('20260923')).closest('tr')!
    expect(
      within(logs).getByText(
        new Date('2026-10-25T10:00:00Z').toLocaleString(undefined, {
          dateStyle: 'medium',
          timeStyle: 'short',
        }),
      ),
    ).toBeInTheDocument()
    const alerts = screen.getByText('20260915').closest('tr')!
    expect(within(alerts).getByText('Alerts')).toBeInTheDocument()
    expect(within(alerts).getByText('Kept until deleted by hand')).toBeInTheDocument()
    expect(
      screen.getByText(
        'Alerts: kept in the database for 10 days (searchable), then as gzip files, each deleted 10 days after it is written.',
      ),
    ).toBeInTheDocument()
  })

  it('marks the size of today as estimated and a day not counted yet as measuring', async () => {
    renderPanel()
    const today = (await screen.findAllByText('20260925', { selector: 'td' }))[0]!.closest('tr')!
    expect(within(today).getByText(/^≈ /)).toBeInTheDocument()
    const counting = screen.getByText('20260922', { selector: 'td' }).closest('tr')!
    expect(within(counting).getByText('measuring…')).toBeInTheDocument()
  })

  it('says when each day leaves the database', async () => {
    renderPanel()
    const when = (iso: string) =>
      new Date(iso).toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' })
    const day = (await screen.findAllByText('20260924', { selector: 'td' }))[0]!.closest('tr')!
    expect(
      within(day).getByText(`dailyLogs: to a file ${when('2026-09-27T00:54:00Z')}`),
    ).toBeInTheDocument()
    expect(
      within(day).getByText(`auditLogs: dropped ${when('2026-10-02T00:54:00Z')}`),
    ).toBeInTheDocument()
    const section = screen.getByText('Days of alerts in the database').parentElement!
    const alerts = within(section).getByText('20260924').closest('tr')!
    expect(
      within(alerts).getByText(`To a file ${when('2026-10-05T00:54:00Z')}`),
    ).toBeInTheDocument()
  })

  it('archives or deletes a past day of alerts, never today', async () => {
    renderPanel()
    const section = (await screen.findByText('Days of alerts in the database')).parentElement!
    const today = within(section).getByText('20260925').closest('tr')!
    expect(within(today).queryByRole('button', { name: 'Delete these alerts' })).toBeNull()

    const past = within(section).getByText('20260924').closest('tr')!
    expect(within(past).getByText('3,370')).toBeInTheDocument()
    await userEvent.click(within(past).getByRole('button', { name: /Archive/ }))
    await waitFor(() =>
      expect(calls).toContainEqual({ url: '/api/alert-days/20260924/archive', method: 'POST' }),
    )

    await userEvent.click(
      within(within(section).getByText('20260924').closest('tr')!).getByRole('button', {
        name: 'Delete these alerts',
      }),
    )
    const dialog = await screen.findByRole('dialog')
    await userEvent.type(within(dialog).getByLabelText('Day to delete'), '20260924')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Delete day' }))
    await waitFor(() =>
      expect(calls).toContainEqual({ url: '/api/alert-days/20260924', method: 'DELETE' }),
    )
  })

  it('shows how much the disk under the database and archive has left', async () => {
    renderPanel()
    expect(
      await screen.findByText('Disk: 61.2 GB free of 600.0 GB (holds the archive, database)'),
    ).toBeInTheDocument()
  })
})
