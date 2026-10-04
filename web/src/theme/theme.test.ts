import { describe, expect, it } from 'vitest'

import { buildTheme, echartsTheme } from './theme'
import { tokens, type ResolvedMode } from './tokens'

const modes: ResolvedMode[] = ['light', 'dark']

describe('theme', () => {
  it.each(modes)('builds a MUI theme for %s from the tokens', (mode) => {
    const theme = buildTheme(mode)
    expect(theme.palette.mode).toBe(mode)
    expect(theme.palette.background.default).toBe(tokens[mode].background)
    expect(theme.palette.error.main).toBe(tokens[mode].level.error)
  })

  // ECharts inherits nothing from CSS, so a mode switch that does not reach
  // the chart palette is a bug that is easy to miss.
  it.each(modes)('derives the ECharts palette for %s from the same tokens', (mode) => {
    const chart = echartsTheme(mode)
    expect(chart.color).toEqual(tokens[mode].series)
    expect(chart.textStyle.color).toBe(tokens[mode].text)
    expect(chart.levelColors).toEqual(tokens[mode].level)
    expect(chart.backgroundColor).toBe('transparent')
  })

  it('uses a different palette in each mode', () => {
    expect(echartsTheme('light').color).not.toEqual(echartsTheme('dark').color)
    expect(buildTheme('light').palette.text.primary).not.toBe(
      buildTheme('dark').palette.text.primary,
    )
  })

  it('gives every mode the same set of level colours', () => {
    expect(Object.keys(tokens.light.level)).toEqual(Object.keys(tokens.dark.level))
  })

  it.each(modes)('stripes table rows and marks the one under the pointer in %s', (mode) => {
    const theme = buildTheme(mode)
    const root = theme.components?.MuiTableRow?.styleOverrides?.root
    const styles = (typeof root === 'function' ? root({ theme } as never) : root) as Record<
      string,
      { backgroundColor: string }
    >
    const stripe = styles['&:where(tbody:not(.hm-paired-rows) > :nth-of-type(even))']
    const hover = styles['tbody > &:hover, tbody > &.MuiTableRow-hover:hover']
    expect(stripe?.backgroundColor).toBe(theme.palette.action.hover)
    expect(hover?.backgroundColor).toBeTruthy()
    expect(hover?.backgroundColor).not.toBe(stripe?.backgroundColor)
    expect(styles['&:where(tbody.hm-paired-rows > :nth-of-type(4n+3))']).toEqual(stripe)
  })
})
