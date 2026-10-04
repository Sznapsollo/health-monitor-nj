import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import i18n from '../i18n'
import { ThemeModeProvider } from '../theme/ThemeModeProvider'
import { LoginGate } from './LoginGate'

function renderGate() {
  return render(
    <ThemeModeProvider>
      <LoginGate>
        <div>{'the dashboard'}</div>
      </LoginGate>
    </ThemeModeProvider>,
  )
}

describe('LoginGate', () => {
  beforeEach(async () => {
    await i18n.changeLanguage('en')
  })

  it('shows the dashboard when no password is configured', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => ({
        ok: true,
        json: async () => ({ required: false, authenticated: true, readOnly: false }),
      })),
    )
    renderGate()
    expect(await screen.findByText('the dashboard')).toBeInTheDocument()
  })

  it('asks for a password when the server requires one', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => ({
        ok: true,
        json: async () => ({ required: true, authenticated: false, readOnly: false }),
      })),
    )
    renderGate()
    expect(await screen.findByLabelText('Password')).toBeInTheDocument()
    expect(screen.queryByText('the dashboard')).not.toBeInTheDocument()
  })

  it('shows and hides the password on request', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => ({
        ok: true,
        json: async () => ({ required: true, authenticated: false, readOnly: false }),
      })),
    )
    renderGate()
    const field = await screen.findByLabelText('Password')
    await userEvent.type(field, 'letmein')
    expect(field).toHaveAttribute('type', 'password')

    await userEvent.click(screen.getByRole('button', { name: 'Show password' }))
    expect(field).toHaveAttribute('type', 'text')
    expect(field).toHaveValue('letmein')

    await userEvent.click(screen.getByRole('button', { name: 'Hide password' }))
    expect(field).toHaveAttribute('type', 'password')
  })

  it('shows the dashboard after a successful login', async () => {
    const fetchMock = vi.fn(async (url: string) => {
      if (String(url).includes('/api/login')) {
        return {
          ok: true,
          json: async () => ({
            required: true,
            authenticated: true,
            readOnly: false,
            name: 'anna',
          }),
        }
      }
      return {
        ok: true,
        json: async () => ({ required: true, authenticated: false, readOnly: false }),
      }
    })
    vi.stubGlobal('fetch', fetchMock)

    renderGate()
    await userEvent.type(await screen.findByLabelText('Password'), 'letmein')
    await userEvent.click(screen.getByRole('button', { name: 'Log in' }))
    expect(await screen.findByText('the dashboard')).toBeInTheDocument()
  })

  it('says so when the password is wrong', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string) => {
        if (String(url).includes('/api/login')) {
          return { ok: false, status: 401, json: async () => ({ error: 'that did not work' }) }
        }
        return {
          ok: true,
          json: async () => ({ required: true, authenticated: false, readOnly: false }),
        }
      }),
    )
    renderGate()
    await userEvent.type(await screen.findByLabelText('Password'), 'nope')
    await userEvent.click(screen.getByRole('button', { name: 'Log in' }))
    expect(await screen.findByText('that did not work')).toBeInTheDocument()
  })

  it('offers a retry instead of the dashboard when the server cannot be reached', async () => {
    let up = false
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => {
        if (!up) throw new Error('connection refused')
        return {
          ok: true,
          json: async () => ({ required: false, authenticated: true, readOnly: false }),
        }
      }),
    )
    renderGate()
    expect(
      await screen.findByText('The monitor could not be reached to check your session.'),
    ).toBeInTheDocument()
    expect(screen.queryByText('the dashboard')).not.toBeInTheDocument()

    up = true
    await userEvent.click(screen.getByRole('button', { name: 'Try again' }))
    expect(await screen.findByText('the dashboard')).toBeInTheDocument()
  })

  it('does not let a server error through as a session', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => ({ ok: false, status: 503, json: async () => ({}) })),
    )
    renderGate()
    expect(await screen.findByRole('button', { name: 'Try again' })).toBeInTheDocument()
    expect(screen.queryByText('the dashboard')).not.toBeInTheDocument()
  })

  it('explains an unreachable server in Polish', async () => {
    await i18n.changeLanguage('pl')
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => {
        throw new Error('connection refused')
      }),
    )
    renderGate()
    expect(await screen.findByRole('button', { name: 'Spróbuj ponownie' })).toBeInTheDocument()
  })
})
