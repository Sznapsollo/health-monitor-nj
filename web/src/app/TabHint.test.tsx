import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import en from '../i18n/en.json'
import i18n from '../i18n'
import pl from '../i18n/pl.json'
import { README_URL, TabHint } from './TabHint'

describe('TabHint', () => {
  it('says what the tab is for and links to its README section', async () => {
    await i18n.changeLanguage('en')
    render(<TabHint tab="activity" />)
    expect(screen.getByText(/Who used the monitored platform/)).toBeInTheDocument()
    expect(
      screen.getByRole('link', { name: 'README → The tabs → Platform users' }),
    ).toHaveAttribute('href', `${README_URL}#platform-users`)
  })

  it('has a hint in every language for every tab', () => {
    expect(Object.keys(pl.hints).sort()).toEqual(Object.keys(en.hints).sort())
  })
})
