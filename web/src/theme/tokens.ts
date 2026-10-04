// One palette per mode. Nothing outside this file may hard-code a colour:
// MUI components read it through the theme, and ECharts (which inherits
// nothing from CSS) reads it through echartsTheme().
export type ThemeMode = 'light' | 'dark' | 'system'
export type ResolvedMode = 'light' | 'dark'

export interface LevelColors {
  error: string
  warn: string
  info: string
  debug: string
  ok: string
}

export interface Tokens {
  background: string
  paper: string
  text: string
  textMuted: string
  divider: string
  primary: string
  level: LevelColors
  /** Categorical series colours, in the order charts assign them. */
  series: string[]
}

export const tokens: Record<ResolvedMode, Tokens> = {
  light: {
    background: '#f5f6f8',
    paper: '#ffffff',
    text: '#1c1f23',
    textMuted: '#5b6570',
    divider: '#e0e3e7',
    primary: '#1565c0',
    level: {
      error: '#c62828',
      warn: '#ef6c00',
      info: '#1565c0',
      debug: '#6a737d',
      ok: '#2e7d32',
    },
    series: [
      '#1565c0',
      '#2e7d32',
      '#ef6c00',
      '#6a1b9a',
      '#00838f',
      '#c62828',
      '#4e342e',
      '#37474f',
      '#9e9d24',
      '#ad1457',
    ],
  },
  dark: {
    background: '#12151a',
    paper: '#1a1f27',
    text: '#e6e9ee',
    textMuted: '#9aa4b1',
    divider: '#2b323c',
    primary: '#64b5f6',
    level: {
      error: '#ef5350',
      warn: '#ffa726',
      info: '#64b5f6',
      debug: '#9aa4b1',
      ok: '#66bb6a',
    },
    series: [
      '#64b5f6',
      '#66bb6a',
      '#ffa726',
      '#ba68c8',
      '#4dd0e1',
      '#ef5350',
      '#a1887f',
      '#90a4ae',
      '#d4e157',
      '#f06292',
    ],
  },
}

export const THEME_STORAGE_KEY = 'hm.theme'
