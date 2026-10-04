import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import i18n from '../i18n'
import { ThemeModeProvider } from '../theme/ThemeModeProvider'
import { RulesPanel } from './RulesPanel'

const rules = {
  platform: 'test',
  latency: { defaultMs: 1000, level: 'WARN', overrides: [] },
  offline: { afterSeconds: 300, repeatSeconds: 300 },
  groups: { windowSeconds: 60 },
}

describe('RulesPanel', () => {
  beforeEach(async () => {
    await i18n.changeLanguage('en')
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string) => ({
        ok: true,
        status: 200,
        json: async () =>
          String(url).includes('/api/rules/test')
            ? { platform: 'test', minutes: 60, rows: 0, total: 0, byRule: null, examples: null }
            : rules,
      })),
    )
  })

  afterEach(() => vi.unstubAllGlobals())

  it('explains how to add a rule when opened, and shows an example pattern', async () => {
    render(
      <ThemeModeProvider>
        <RulesPanel platform="test" />
      </ThemeModeProvider>,
    )
    await userEvent.click(await screen.findByRole('button', { name: 'How to add a rule' }))
    expect(await screen.findByText(/press Add override/)).toBeVisible()
    expect(screen.getByRole('link', { name: 'README → Writing alert rules' })).toHaveAttribute(
      'href',
      expect.stringContaining('#writing-alert-rules'),
    )

    await userEvent.click(screen.getByRole('button', { name: 'Add override' }))
    expect(screen.getByPlaceholderText('/api/reports/')).toBeInTheDocument()
  })

  it('reports a test with nothing to test instead of failing', async () => {
    render(
      <ThemeModeProvider>
        <RulesPanel platform="test" />
      </ThemeModeProvider>,
    )
    await userEvent.click(await screen.findByRole('button', { name: 'Test against the last hour' }))
    expect(await screen.findByText('0 alerts from 0 rows')).toBeInTheDocument()
  })

  it('turns the reminders off with 0 minutes', async () => {
    const puts: unknown[] = []
    vi.stubGlobal(
      'fetch',
      vi.fn(async (_url: string, init?: RequestInit) => {
        if (init?.method === 'PUT') puts.push(JSON.parse(String(init.body)))
        return { ok: true, status: 200, json: async () => rules }
      }),
    )
    render(
      <ThemeModeProvider>
        <RulesPanel platform="test" />
      </ThemeModeProvider>,
    )
    const remind = await screen.findByLabelText('Remind every (minutes)')
    expect(remind).toHaveValue(5)
    await userEvent.clear(remind)
    await userEvent.type(remind, '0')
    await userEvent.click(screen.getByRole('button', { name: 'Save rules' }))
    await vi.waitFor(() =>
      expect(puts).toContainEqual(
        expect.objectContaining({ offline: { afterSeconds: 300, repeatSeconds: 0 } }),
      ),
    )
  })

  it("does not offer the previous platform's rules for saving while the next one loads", async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn((url: string) =>
        String(url).includes('/api/rules?platform=b')
          ? new Promise(() => {})
          : Promise.resolve({ ok: true, status: 200, json: async () => rules }),
      ),
    )
    const { rerender } = render(
      <ThemeModeProvider>
        <RulesPanel platform="a" />
      </ThemeModeProvider>,
    )
    expect(await screen.findByRole('button', { name: 'Save rules' })).toBeInTheDocument()

    rerender(
      <ThemeModeProvider>
        <RulesPanel platform="b" />
      </ThemeModeProvider>,
    )
    expect(screen.queryByRole('button', { name: 'Save rules' })).not.toBeInTheDocument()
  })
})
