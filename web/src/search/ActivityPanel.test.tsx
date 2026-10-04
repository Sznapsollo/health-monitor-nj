import { fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import i18n from '../i18n'
import { ThemeModeProvider } from '../theme/ThemeModeProvider'
import { ActivityPanel } from './ActivityPanel'

const activity = Array.from({ length: 60 }, (_, i) => ({
  day: '20260926',
  account: `acc-${i}`,
  user: `user-${i}@example.test`,
  minutes: 60 - i,
  events: 100 - i,
}))

describe('ActivityPanel', () => {
  beforeEach(async () => {
    await i18n.changeLanguage('en')
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string) => ({
        ok: true,
        headers: new Headers({ 'content-type': 'application/json' }),
        json: async () => (String(url).includes('/days') ? { days: ['20260926'] } : { activity }),
      })),
    )
  })

  afterEach(() => vi.unstubAllGlobals())

  it('shows a page at a time, numbered across pages', async () => {
    render(
      <ThemeModeProvider>
        <ActivityPanel />
      </ThemeModeProvider>,
    )
    expect(await screen.findByText('user-0@example.test')).toBeInTheDocument()
    expect(screen.getByText('user-49@example.test')).toBeInTheDocument()
    expect(screen.queryByText('user-50@example.test')).not.toBeInTheDocument()
    expect(screen.getByText('1–50 of 60')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /next page/i }))
    const row = (await screen.findByText('user-50@example.test')).closest('tr')!
    expect(row.textContent).toContain('51')
  })
})
