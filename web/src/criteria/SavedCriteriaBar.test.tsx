import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import i18n from '../i18n'
import { useMonitorStore } from '../store/useMonitorStore'
import { ThemeModeProvider } from '../theme/ThemeModeProvider'
import type { Criteria } from '../ws/types'
import { SavedCriteriaBar } from './SavedCriteriaBar'
import { loadSaved, saveNamed } from './persistence'

const criteria: Criteria[] = [
  { signal: 'requests', historyMinutes: 180, group: 'url', groupTop: 10, sortBy: 'count' },
]

const catalogue = {
  platforms: [
    {
      name: 'example',
      signals: [
        {
          name: 'requests',
          kind: 'timeseries',
          displayName: 'Requests',
          dims: [{ name: 'url', displayName: 'URL' }],
          retention: { hotDetailMinutes: 120, hotTotalsMinutes: 2880, durableDays: 30 },
        },
      ],
    },
  ],
}

/** What the signal's own definition asks for. */
const defaults: Criteria[] = [
  {
    signal: 'requests',
    historyMinutes: 60,
    groupMinutes: 120,
    group: '',
    groupTop: 50,
    sortBy: 'count',
  },
]

function renderBar(
  onApply = vi.fn(),
  current: Criteria[] = criteria,
  visible: string[] = ['requests'],
) {
  render(
    <ThemeModeProvider>
      <SavedCriteriaBar platform="example" criteria={current} visible={visible} onApply={onApply} />
    </ThemeModeProvider>,
  )
  return onApply
}

describe('saved filter sets', () => {
  beforeEach(async () => {
    await i18n.changeLanguage('en')
    globalThis.localStorage?.clear()
    useMonitorStore.setState({
      activeView: null,
      criteria: {},
      views: {},
      platform: 'example',
      catalogue: catalogue as never,
    })
    vi.stubGlobal('history', { replaceState: vi.fn() })
  })

  it('saves the current filters and makes them the active set', async () => {
    const onApply = renderBar()
    await userEvent.type(screen.getByLabelText('Name'), 'slow pages')
    await userEvent.click(screen.getByRole('button', { name: 'Save' }))

    expect(loadSaved().map((s) => s.name)).toEqual(['slow pages'])
    // A set is the group of charts as well as their filters.
    expect(loadSaved()[0].visible).toEqual(['requests'])
    // Saving applies it too, so the picker and the address bar agree with
    // what is on screen.
    expect(onApply).toHaveBeenCalledWith(criteria, 'slow pages', ['requests'])
  })

  it('loads a saved set from the picker', async () => {
    saveNamed({ name: 'mornings', platform: 'example', criteria, visible: ['requests'] })
    const onApply = renderBar()

    await userEvent.click(screen.getByRole('combobox'))
    await userEvent.click(await screen.findByRole('option', { name: 'mornings' }))
    // The set brings its charts back with it, not just their filters.
    expect(onApply).toHaveBeenCalledWith(criteria, 'mornings', ['requests'])
  })

  it('shows which set is active after a reload', () => {
    saveNamed({ name: 'mornings', platform: 'example', criteria })
    useMonitorStore.setState({ activeView: 'mornings' })
    renderBar()
    expect(screen.getByRole('combobox')).toHaveTextContent('mornings')
  })

  it('clears back to the defaults', async () => {
    const reset = vi.fn()
    useMonitorStore.setState({ resetCriteria: reset })
    renderBar()
    await userEvent.click(screen.getByLabelText('Back to the defaults'))
    expect(reset).toHaveBeenCalled()
  })

  it('does not offer to clear what is already default', () => {
    renderBar(vi.fn(), defaults)
    // An action that would change nothing should not be on screen.
    expect(screen.queryByLabelText('Back to the defaults')).not.toBeInTheDocument()
  })

  it('offers to clear once a filter differs from the default', () => {
    renderBar(vi.fn(), [{ ...defaults[0], group: 'url' }])
    expect(screen.getByLabelText('Back to the defaults')).toBeInTheDocument()
  })

  it('offers to clear while a saved set is applied, even if it matches the defaults', () => {
    useMonitorStore.setState({ activeView: 'mornings' })
    renderBar(vi.fn(), defaults)
    // Clearing then means "stop following that set", which is a real change.
    expect(screen.getByLabelText('Back to the defaults')).toBeInTheDocument()
  })

  it('deletes only when a set is selected', async () => {
    saveNamed({ name: 'mornings', platform: 'example', criteria })
    const reset = vi.fn()
    useMonitorStore.setState({ resetCriteria: reset })

    renderBar()
    expect(screen.getByRole('button', { name: 'Delete this saved set' })).toBeDisabled()

    useMonitorStore.setState({ activeView: 'mornings' })
    const remove = () => screen.getByRole('button', { name: 'Delete this saved set' })
    await waitFor(() => expect(remove()).toBeEnabled())
    await userEvent.click(remove())
    expect(loadSaved()).toEqual([])
    expect(reset).toHaveBeenCalled()
  })

  it('copies a link naming the set when one is active', async () => {
    const writeText = vi.fn<(text: string) => Promise<void>>()
    vi.stubGlobal('navigator', { clipboard: { writeText } })
    useMonitorStore.setState({ activeView: 'mornings' })

    renderBar()
    await userEvent.click(screen.getByLabelText('Copy a link'))
    await waitFor(() => expect(writeText).toHaveBeenCalled())
    expect(String(writeText.mock.calls[0]?.[0])).toContain('view=mornings')
  })

  it('copies a link describing the filters when no set is active', async () => {
    const writeText = vi.fn<(text: string) => Promise<void>>()
    vi.stubGlobal('navigator', { clipboard: { writeText } })

    renderBar()
    await userEvent.click(screen.getByLabelText('Copy a link'))
    await waitFor(() => expect(writeText).toHaveBeenCalled())
    const url = String(writeText.mock.calls[0]?.[0])
    // Describes itself, so it works in someone else's browser.
    expect(url).toContain('signal=requests')
    expect(url).toContain('group=url')
    expect(url).not.toContain('view=')
  })
})
