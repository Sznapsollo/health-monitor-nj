import CssBaseline from '@mui/material/CssBaseline'
import { ThemeProvider } from '@mui/material/styles'
import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react'

import { ThemeModeContext } from './ThemeModeContext'
import { buildTheme } from './theme'
import { THEME_STORAGE_KEY, type ResolvedMode, type ThemeMode } from './tokens'

function readStoredMode(): ThemeMode {
  try {
    const stored = globalThis.localStorage?.getItem(THEME_STORAGE_KEY)
    return stored === 'light' || stored === 'dark' || stored === 'system' ? stored : 'system'
  } catch {
    return 'system'
  }
}

function systemMode(): ResolvedMode {
  return globalThis.matchMedia?.('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
}

export function ThemeModeProvider({ children }: { children: ReactNode }) {
  const [mode, setModeState] = useState<ThemeMode>(readStoredMode)
  const [system, setSystem] = useState<ResolvedMode>(systemMode)

  useEffect(() => {
    const query = globalThis.matchMedia?.('(prefers-color-scheme: dark)')
    if (!query) return
    const onChange = () => setSystem(query.matches ? 'dark' : 'light')
    query.addEventListener('change', onChange)
    return () => query.removeEventListener('change', onChange)
  }, [])

  const setMode = useCallback((next: ThemeMode) => {
    setModeState(next)
    try {
      globalThis.localStorage?.setItem(THEME_STORAGE_KEY, next)
    } catch {
      // storage may be blocked
    }
  }, [])

  const resolved: ResolvedMode = mode === 'system' ? system : mode
  const theme = useMemo(() => buildTheme(resolved), [resolved])
  const value = useMemo(() => ({ mode, resolved, setMode }), [mode, resolved, setMode])

  return (
    <ThemeModeContext.Provider value={value}>
      <ThemeProvider theme={theme}>
        <CssBaseline enableColorScheme />
        {children}
      </ThemeProvider>
    </ThemeModeContext.Provider>
  )
}
