import { render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import i18n from '../i18n'
import { ThemeModeProvider } from '../theme/ThemeModeProvider'
import { VersionTag } from './VersionTag'

describe('VersionTag', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('shows the running version and commit', async () => {
    await i18n.changeLanguage('en')
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => ({
        ok: true,
        json: async () => ({ version: '1.0.0', commit: 'f951f06', goVersion: 'go1.26.0' }),
      })),
    )
    render(
      <ThemeModeProvider>
        <VersionTag />
      </ThemeModeProvider>,
    )
    expect(await screen.findByText('health-monitor-nj · v1.0.0 · f951f06')).toHaveAttribute(
      'aria-label',
      'health-monitor-nj · Version 1.0.0, commit f951f06, built with go1.26.0',
    )
  })
})
