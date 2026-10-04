import { render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import i18n from '../i18n'
import { ThemeModeProvider } from '../theme/ThemeModeProvider'
import { BackupsSection } from './BackupsSection'

function backup(n: number) {
  return {
    file: `hm-backup-20260924-12000${n}.db`,
    bytes: 1024 * 1024,
    created: '2026-09-24T12:00:00Z',
  }
}

function serve(count: number) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => ({
      ok: true,
      headers: new Headers({ 'content-type': 'application/json' }),
      json: async () => ({ backups: Array.from({ length: count }, (_, i) => backup(i)), limit: 3 }),
    })),
  )
}

function renderSection() {
  render(
    <ThemeModeProvider>
      <BackupsSection />
    </ThemeModeProvider>,
  )
}

describe('BackupsSection', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('stops new backups at the limit and says how to make room', async () => {
    await i18n.changeLanguage('en')
    serve(3)
    renderSection()

    expect(await screen.findByText(/Up to 3 backups are kept/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Back up the database/ })).toBeDisabled()
  })

  it('allows a backup below the limit', async () => {
    await i18n.changeLanguage('en')
    serve(2)
    renderSection()

    expect(await screen.findByText('hm-backup-20260924-120001.db')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Back up the database/ })).toBeEnabled()
    expect(screen.queryByText(/Up to 3 backups are kept/)).not.toBeInTheDocument()
  })
})
