import { alpha, createTheme, type Theme } from '@mui/material/styles'

import { tokens, type ResolvedMode } from './tokens'

/**
 * Marks a table body whose entries are two rows each (a line and its detail
 * row), so they are striped in pairs rather than one row at a time.
 */
export const PAIRED_ROWS = 'hm-paired-rows'

/** The MUI theme for a resolved mode. */
export function buildTheme(mode: ResolvedMode): Theme {
  const t = tokens[mode]
  return createTheme({
    palette: {
      mode,
      primary: { main: t.primary },
      background: { default: t.background, paper: t.paper },
      text: { primary: t.text, secondary: t.textMuted },
      divider: t.divider,
      error: { main: t.level.error },
      warning: { main: t.level.warn },
      info: { main: t.level.info },
      success: { main: t.level.ok },
    },
    shape: { borderRadius: 8 },
    typography: {
      fontFamily: ['Roboto', 'system-ui', 'Segoe UI', 'Helvetica', 'Arial', 'sans-serif'].join(','),
      fontSize: 13,
    },
    components: {
      MuiTableRow: {
        styleOverrides: {
          root: ({ theme }) => {
            const stripe = { backgroundColor: theme.palette.action.hover }
            const hover = { backgroundColor: alpha(theme.palette.primary.main, 0.14) }
            // :where() takes the stripes' specificity away, so the hover and
            // a selected row always win over them.
            return {
              [`&:where(tbody:not(.${PAIRED_ROWS}) > :nth-of-type(even))`]: stripe,
              [`&:where(tbody.${PAIRED_ROWS} > :nth-of-type(4n+3))`]: stripe,
              [`&:where(tbody.${PAIRED_ROWS} > :nth-of-type(4n+4))`]: stripe,
              'tbody > &:hover, tbody > &.MuiTableRow-hover:hover': hover,
            }
          },
        },
      },
    },
  })
}

/**
 * ECharts draws to canvas and inherits nothing from CSS, so every chart takes
 * its colours from here and re-renders when the mode changes.
 */
export function echartsTheme(mode: ResolvedMode) {
  const t = tokens[mode]
  return {
    color: t.series,
    backgroundColor: 'transparent',
    textStyle: { color: t.text },
    title: { textStyle: { color: t.text } },
    legend: { textStyle: { color: t.textMuted } },
    tooltip: {
      backgroundColor: t.paper,
      borderColor: t.divider,
      textStyle: { color: t.text },
    },
    categoryAxis: axis(t.divider, t.textMuted),
    valueAxis: axis(t.divider, t.textMuted),
    timeAxis: axis(t.divider, t.textMuted),
    levelColors: t.level,
  }
}

function axis(line: string, label: string) {
  return {
    axisLine: { lineStyle: { color: line } },
    axisTick: { lineStyle: { color: line } },
    axisLabel: { color: label },
    splitLine: { lineStyle: { color: line } },
  }
}
