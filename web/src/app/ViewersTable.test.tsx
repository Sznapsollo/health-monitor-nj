import { render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import i18n from '../i18n'
import { ThemeModeProvider } from '../theme/ThemeModeProvider'
import { ViewersTable } from './ViewersTable'

describe('loading indicator', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('shows that data is on its way, then what came back', async () => {
    await i18n.changeLanguage('en')
    let answer: (v: unknown) => void = () => {}
    vi.stubGlobal(
      'fetch',
      vi.fn(
        () =>
          new Promise((resolve) => {
            answer = resolve
          }),
      ),
    )
    render(
      <ThemeModeProvider>
        <ViewersTable />
      </ThemeModeProvider>,
    )
    expect(screen.getByRole('status')).toHaveTextContent('Loading…')

    answer({ ok: true, json: async () => ({ sessions: [] }) })
    expect(await screen.findByText(i18n.t('settings.noViewers'))).toBeInTheDocument()
    expect(screen.queryByRole('status')).not.toBeInTheDocument()
  })
})
