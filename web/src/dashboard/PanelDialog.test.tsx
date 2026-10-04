import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

import i18n from '../i18n'
import { ThemeModeProvider } from '../theme/ThemeModeProvider'
import { PanelDialog } from './PanelDialog'

describe('PanelDialog', () => {
  it('keeps what was picked when the page re-renders with a new empty panel', async () => {
    await i18n.changeLanguage('en')
    const props = { open: true, specs: [], onClose: vi.fn(), onSave: vi.fn() }
    const { rerender } = render(
      <ThemeModeProvider>
        <PanelDialog {...props} panel={{ type: 'chart' }} />
      </ThemeModeProvider>,
    )
    fireEvent.change(screen.getByLabelText('Title (optional)'), { target: { value: 'Regions' } })

    rerender(
      <ThemeModeProvider>
        <PanelDialog {...props} panel={{ type: 'chart' }} />
      </ThemeModeProvider>,
    )
    expect(screen.getByLabelText('Title (optional)')).toHaveValue('Regions')
  })

  it('starts from the given panel each time it opens', async () => {
    await i18n.changeLanguage('en')
    const props = { specs: [], onClose: vi.fn(), onSave: vi.fn() }
    const { rerender } = render(
      <ThemeModeProvider>
        <PanelDialog {...props} open panel={{ type: 'chart', title: 'First' }} />
      </ThemeModeProvider>,
    )
    rerender(
      <ThemeModeProvider>
        <PanelDialog {...props} open={false} panel={{ type: 'chart', title: 'First' }} />
      </ThemeModeProvider>,
    )
    rerender(
      <ThemeModeProvider>
        <PanelDialog {...props} open panel={{ type: 'chart', title: 'Second' }} />
      </ThemeModeProvider>,
    )
    expect(screen.getByLabelText('Title (optional)')).toHaveValue('Second')
  })
})
