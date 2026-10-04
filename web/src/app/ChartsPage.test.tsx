import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { SignalSpec } from '../api/catalogue'
import i18n from '../i18n'
import { useMonitorStore } from '../store/useMonitorStore'
import { ThemeModeProvider } from '../theme/ThemeModeProvider'
import { ChartsPage } from './ChartsPage'

vi.mock('../charts/echarts', () => ({
  init: () => ({ setOption: vi.fn(), resize: vi.fn(), dispose: vi.fn() }),
}))

const spec = (name: string, displayName: string, kind = 'timeseries') =>
  ({
    name,
    kind,
    displayName,
    dims: [],
    retention: { hotDetailMinutes: 120, hotTotalsMinutes: 2880, durableDays: 30 },
  }) as SignalSpec

const signals = [spec('requests', 'Requests'), spec('jobs', 'Jobs'), spec('checkout', 'Checkout')]

describe('the Charts tab order', () => {
  beforeEach(async () => {
    await i18n.changeLanguage('en')
    globalThis.localStorage?.clear()
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => ({ ok: true, json: async () => ({ gauges: [] }) })),
    )
    useMonitorStore.setState({
      platform: 'example',
      catalogue: { platforms: [{ name: 'example', signals }] },
      criteria: {
        requests: { signal: 'requests' },
        jobs: { signal: 'jobs' },
        checkout: { signal: 'checkout' },
      },
      views: {},
      visible: ['checkout', 'requests'],
      dashboards: [],
      dashboardId: null,
    })
  })
  afterEach(() => vi.unstubAllGlobals())

  const headings = () => screen.getAllByRole('heading', { level: 2 }).map((h) => h.textContent)

  it('draws the picked charts in the picked order and moves them', async () => {
    render(
      <ThemeModeProvider>
        <ChartsPage signals={signals} />
      </ThemeModeProvider>,
    )
    expect(headings()).toEqual(['Checkout', 'Requests'])

    const requests = screen.getByRole('heading', { name: 'Requests' }).parentElement as HTMLElement
    await userEvent.click(within(requests).getByRole('button', { name: 'Move up' }))
    expect(headings()).toEqual(['Requests', 'Checkout'])
    expect(
      within(
        screen.getByRole('heading', { name: 'Requests' }).parentElement as HTMLElement,
      ).getByRole('button', { name: 'Move up' }),
    ).toBeDisabled()
  })
})
