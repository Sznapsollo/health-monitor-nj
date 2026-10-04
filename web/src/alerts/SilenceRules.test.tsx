import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import i18n from '../i18n'
import { useAlertStore } from '../store/useAlertStore'
import { ThemeModeProvider } from '../theme/ThemeModeProvider'
import { SilenceRules } from './SilenceRules'

describe('SilenceRules', () => {
  let posted: unknown[]
  let calls: { url: string; method: string; body: unknown }[]

  beforeEach(async () => {
    await i18n.changeLanguage('en')
    posted = []
    calls = []
    useAlertStore.setState({ silences: [], review: [], categories: ['Warning JOB', 'latency'] })
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string, init?: RequestInit) => {
        const body = init?.body ? JSON.parse(String(init.body)) : undefined
        calls.push({ url: String(url), method: init?.method ?? 'GET', body })
        if (init?.method === 'POST') posted.push(body)
        return {
          ok: true,
          status: 200,
          json: async () => ({ silences: [], review: [], id: 'sil-1', hidden: 0 }),
        }
      }),
    )
  })

  afterEach(() => vi.unstubAllGlobals())

  async function openDialog() {
    render(
      <ThemeModeProvider>
        <SilenceRules platform="shop" />
      </ThemeModeProvider>,
    )
    await userEvent.click(screen.getByRole('button', { name: 'New silence' }))
    return screen.findByRole('dialog')
  }

  it('mutes alerts containing a text, with the quick reason already chosen', async () => {
    const dialog = await openDialog()
    const create = within(dialog).getByRole('button', { name: 'Silence' })
    expect(create).toBeDisabled()
    await userEvent.type(within(dialog).getByLabelText('Text the message contains'), 'Timeout')
    await userEvent.click(create)
    await waitFor(() =>
      expect(posted).toContainEqual(
        expect.objectContaining({
          platform: 'shop',
          target: 'match:contains="Timeout"',
          kind: 'mute',
          reason: "don't need to see it",
        }),
      ),
    )
  })

  it('silences a whole category', async () => {
    const dialog = await openDialog()
    await userEvent.click(within(dialog).getByLabelText('All alerts of a category'))
    await userEvent.click(within(dialog).getByLabelText('Category'))
    await userEvent.click(await screen.findByRole('option', { name: 'Warning JOB' }))
    await userEvent.click(within(dialog).getByRole('button', { name: 'Silence' }))
    await waitFor(() =>
      expect(posted).toContainEqual(expect.objectContaining({ target: 'category:Warning JOB' })),
    )
  })

  it('edits a silence, starting from what it is now', async () => {
    useAlertStore.setState({
      silences: [
        {
          id: 'sil-7',
          platform: 'shop',
          target: 'category:Error API',
          kind: 'mute',
          reason: 'known issue',
          by: 'anna',
          created: '2026-09-25T10:00:00Z',
          suppressed: 4,
        },
      ],
    })
    render(
      <ThemeModeProvider>
        <SilenceRules platform="shop" />
      </ThemeModeProvider>,
    )
    await userEvent.click(screen.getByRole('button', { name: 'Edit this silence' }))
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByText('Edit silence')).toBeInTheDocument()
    expect(within(dialog).getByLabelText('All alerts of a category')).toBeChecked()
    expect(within(dialog).getByLabelText(/^Reason/)).toHaveValue('known issue')

    await userEvent.click(within(dialog).getByLabelText('Alerts whose message contains'))
    await userEvent.type(within(dialog).getByLabelText('Text the message contains'), 'deadlock')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(calls).toContainEqual(expect.objectContaining({ method: 'PUT' })))
    const put = calls.find((c) => c.method === 'PUT')!
    expect(put.url).toBe('/api/silences/sil-7')
    expect(put.body).toMatchObject({
      target: 'match:contains="deadlock"',
      kind: 'mute',
      reason: 'known issue',
    })
  })
})
