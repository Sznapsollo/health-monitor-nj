import { render, screen, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import i18n from '../i18n'
import { ThemeModeProvider } from '../theme/ThemeModeProvider'
import { VisitsTable } from './VisitsTable'

const visits = [
  {
    id: 'v-2',
    login: 'hall',
    kind: 'display',
    ip: '192.168.0.144',
    userAgent: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) Chrome/153.0.0.0 Safari/537.36',
    signals: ['requests'],
    started: '2026-09-26T06:00:00Z',
    lastSeen: '2026-09-26T08:00:00Z',
    reconnects: 3,
    sent: 12400,
    open: true,
  },
  {
    id: 'v-1',
    login: 'nj',
    kind: 'user',
    ip: '172.19.0.1',
    userAgent: 'Mozilla/5.0 (X11; Linux x86_64) Firefox/131.0',
    signals: ['requests', 'checkout'],
    started: '2026-09-26T05:00:00Z',
    lastSeen: '2026-09-26T06:30:00Z',
    ended: '2026-09-26T06:30:00Z',
    reconnects: 0,
    sent: 900,
    open: false,
  },
]

describe('VisitsTable', () => {
  beforeEach(async () => {
    await i18n.changeLanguage('en')
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string) => ({
        ok: true,
        json: async () =>
          String(url).includes('/days') ? { days: ['20260926', '20260925'] } : { visits },
      })),
    )
  })

  afterEach(() => vi.unstubAllGlobals())

  it('lists each visit with who, how long and how often it reconnected', async () => {
    render(
      <ThemeModeProvider>
        <VisitsTable />
      </ThemeModeProvider>,
    )
    const wall = (await screen.findByText('hall')).closest('tr')!
    expect(within(wall).getByText('Wall display')).toBeInTheDocument()
    expect(within(wall).getByText('still open')).toBeInTheDocument()
    expect(within(wall).getByText('Chrome 153 · macOS')).toBeInTheDocument()
    expect(within(wall).getByText('3')).toBeInTheDocument()

    const mine = screen.getByText('nj').closest('tr')!
    expect(within(mine).getByText('1 h 30 min')).toBeInTheDocument()
    expect(within(mine).getByText('Firefox 131 · Linux')).toBeInTheDocument()
    expect(within(mine).getByText('requests, checkout')).toBeInTheDocument()
  })
})
