import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it } from 'vitest'

import i18n from '../i18n'
import { ThemeModeProvider } from '../theme/ThemeModeProvider'
import { ThemeToggle } from './ThemeToggle'

function renderToggle() {
  return render(
    <ThemeModeProvider>
      <ThemeToggle />
    </ThemeModeProvider>,
  )
}

describe('ThemeToggle', () => {
  beforeEach(async () => {
    await i18n.changeLanguage('en')
    globalThis.localStorage?.clear()
  })

  it('offers light, dark and following the system', () => {
    renderToggle()
    for (const name of ['Light', 'Dark', 'Follow system']) {
      expect(screen.getByRole('button', { name })).toBeInTheDocument()
    }
  })

  it('starts on system, which is a choice rather than the absence of one', () => {
    renderToggle()
    expect(screen.getByRole('button', { name: 'Follow system' })).toHaveAttribute(
      'aria-pressed',
      'true',
    )
  })

  it('remembers the choice for the next visit', async () => {
    renderToggle()
    await userEvent.click(screen.getByRole('button', { name: 'Dark' }))

    expect(screen.getByRole('button', { name: 'Dark' })).toHaveAttribute('aria-pressed', 'true')
    expect(globalThis.localStorage.getItem('hm.theme')).toBe('dark')
  })

  it('keeps the choice when the active mode is clicked again', async () => {
    renderToggle()
    await userEvent.click(screen.getByRole('button', { name: 'Light' }))
    await userEvent.click(screen.getByRole('button', { name: 'Light' }))
    // A toggle group deselects on a second click; a theme must not become
    // "none".
    expect(screen.getByRole('button', { name: 'Light' })).toHaveAttribute('aria-pressed', 'true')
  })

  it('is labelled in Polish too', async () => {
    await i18n.changeLanguage('pl')
    renderToggle()
    expect(screen.getByRole('button', { name: 'Ciemny' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Jak w systemie' })).toBeInTheDocument()
  })
})
