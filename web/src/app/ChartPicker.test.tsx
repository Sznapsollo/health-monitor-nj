import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import i18n from '../i18n'
import type { SignalSpec } from '../api/catalogue'
import { ThemeModeProvider } from '../theme/ThemeModeProvider'
import { ChartPicker } from './ChartPicker'

const specs = [
  { name: 'requests', kind: 'timeseries', displayName: 'Requests', dims: [] },
  { name: 'jobs', kind: 'timeseries', displayName: 'Jobs', dims: [] },
  { name: 'queues', kind: 'gauge', displayName: 'Queues', dims: [] },
] as unknown as SignalSpec[]

function open(visible: string[]) {
  const onChange = vi.fn()
  render(
    <ThemeModeProvider>
      <ChartPicker specs={specs} visible={visible} onChange={onChange} />
    </ThemeModeProvider>,
  )
  return onChange
}

describe('the chart picker', () => {
  beforeEach(async () => {
    await i18n.changeLanguage('en')
  })

  it('says how many of the charts are on show', () => {
    open(['requests'])
    expect(screen.getByRole('combobox')).toHaveTextContent('1 of 3')
  })

  it('unticks a chart', async () => {
    const onChange = open(['requests', 'jobs'])
    await userEvent.click(screen.getByRole('combobox'))
    await userEvent.click(await screen.findByRole('option', { name: 'Jobs' }))
    expect(onChange).toHaveBeenCalledWith(['requests'])
  })

  it('takes everything or nothing in one go', async () => {
    const onChange = open(['requests'])
    await userEvent.click(screen.getByRole('combobox'))
    await userEvent.click(await screen.findByRole('option', { name: 'All' }))
    expect(onChange).toHaveBeenCalledWith(['requests', 'jobs', 'queues'])

    await userEvent.click(await screen.findByRole('option', { name: 'None' }))
    expect(onChange).toHaveBeenLastCalledWith([])
  })

  it('offers current values in their own section and ticks one on', async () => {
    const onChange = open(['requests'])
    await userEvent.click(screen.getByRole('combobox'))
    expect(await screen.findByText('Current values')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('option', { name: 'Queues' }))
    expect(onChange).toHaveBeenCalledWith(['requests', 'queues'])
  })
})
