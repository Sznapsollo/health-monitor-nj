import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import { ThemeModeProvider } from '../theme/ThemeModeProvider'
import { BarGauge } from './BarGauge'
import { gaugeSortOf, sortPoints } from './gaugeSort'

describe('BarGauge', () => {
  it('shows every label with its value', () => {
    render(
      <ThemeModeProvider>
        <BarGauge
          points={[
            { label: 'mail', value: 17 },
            { label: 'invoices', value: 3, warn: true },
            { label: 'exports', value: 1200, unit: 'rows' },
          ]}
        />
      </ThemeModeProvider>,
    )
    expect(screen.getByText('mail')).toBeInTheDocument()
    expect(screen.getByText('17')).toBeInTheDocument()
    expect(screen.getByText('3')).toBeInTheDocument()
    expect(screen.getByText(/1,200 rows/)).toBeInTheDocument()
  })

  it('draws nothing rather than crashing on an empty gauge', () => {
    const { container } = render(
      <ThemeModeProvider>
        <BarGauge points={[]} />
      </ThemeModeProvider>,
    )
    expect(container.querySelectorAll('.MuiLinearProgress-root')).toHaveLength(0)
  })

  it('draws the bars in the order asked for', () => {
    render(
      <ThemeModeProvider>
        <BarGauge
          sort="valueDesc"
          points={[
            { label: 'b', value: 5 },
            { label: 'a', value: 9 },
            { label: 'c', value: 5 },
          ]}
        />
      </ThemeModeProvider>,
    )
    const labels = screen.getAllByText(/^[abc]$/).map((el) => el.textContent)
    expect(labels).toEqual(['a', 'b', 'c'])
  })
})

describe('gauge order', () => {
  const points = [
    { label: 'b', value: 1 },
    { label: 'c', value: 3 },
    { label: 'a', value: 2 },
  ]
  const labels = (sort: Parameters<typeof sortPoints>[1]) =>
    sortPoints(points, sort).map((p) => p.label)

  it('sorts by label or value either way', () => {
    expect(labels('label')).toEqual(['a', 'b', 'c'])
    expect(labels('labelDesc')).toEqual(['c', 'b', 'a'])
    expect(labels('value')).toEqual(['b', 'a', 'c'])
    expect(labels('valueDesc')).toEqual(['c', 'a', 'b'])
  })

  it('takes the panel first, then the signal, then label', () => {
    const spec = {
      name: 'q',
      kind: 'gauge',
      displayName: 'q',
      dims: [],
      views: [{ id: 'bars', type: 'barGauge', options: { sort: 'valueDesc' } }],
      retention: { hotDetailMinutes: 0, hotTotalsMinutes: 0, durableDays: 0 },
    }
    expect(gaugeSortOf(spec, 'labelDesc')).toBe('labelDesc')
    expect(gaugeSortOf(spec)).toBe('valueDesc')
    expect(gaugeSortOf(spec, 'nonsense')).toBe('valueDesc')
    expect(gaugeSortOf(undefined)).toBe('label')
  })
})
