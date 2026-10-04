import { createContext } from 'react'

import type { ResolvedMode, ThemeMode } from './tokens'

export interface ThemeModeContextValue {
  /** What the viewer chose, including "system". */
  mode: ThemeMode
  /** What that resolves to right now. */
  resolved: ResolvedMode
  setMode: (mode: ThemeMode) => void
}

export const ThemeModeContext = createContext<ThemeModeContextValue | null>(null)
