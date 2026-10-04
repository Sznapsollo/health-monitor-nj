import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { ThemeModeProvider } from '../theme/ThemeModeProvider'
import { NoticeBanner } from './NoticeBanner'

function notice(over: Partial<{ level: string; text: string; popup: boolean }> = {}) {
  return { level: 'info', text: 'deploying in five minutes', at: Date.now(), ...over }
}

function renderBanner(props: Parameters<typeof NoticeBanner>[0]) {
  return render(
    <ThemeModeProvider>
      <NoticeBanner {...props} />
    </ThemeModeProvider>,
  )
}

describe('NoticeBanner', () => {
  it('shows the message', () => {
    renderBanner({ notice: notice(), onDismiss: vi.fn() })
    expect(screen.getByText('deploying in five minutes')).toBeInTheDocument()
  })

  it('reads as a person talking, not as an alert', () => {
    renderBanner({ notice: notice({ level: '' }), onDismiss: vi.fn() })
    // Green: a message from a colleague is not another red row to triage.
    expect(screen.getByRole('alert').className).toContain('colorSuccess')
  })

  it('still lets a sender mark something as a warning', () => {
    renderBanner({ notice: notice({ level: 'warn' }), onDismiss: vi.fn() })
    expect(screen.getByRole('alert').className).toContain('colorWarning')
  })

  it('can be dismissed by hand', async () => {
    const onDismiss = vi.fn()
    renderBanner({ notice: notice(), onDismiss })
    await userEvent.click(screen.getByRole('button', { name: /close/i }))
    expect(onDismiss).toHaveBeenCalled()
  })

  it('offers no close button on a wall display', () => {
    // Nobody is standing at it, so nothing may wait to be clicked.
    renderBanner({ notice: notice({ popup: true }), onDismiss: vi.fn(), autoDismissOnly: true })
    expect(screen.queryByRole('button', { name: /close/i })).not.toBeInTheDocument()
  })

  it('shows nothing when there is no message', () => {
    renderBanner({ notice: null, onDismiss: vi.fn() })
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
})
