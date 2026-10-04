import { useContext } from 'react'

import { ThemeModeContext } from './ThemeModeContext'

export function useThemeMode() {
  const ctx = useContext(ThemeModeContext)
  if (!ctx) throw new Error('useThemeMode must be used inside ThemeModeProvider')
  return ctx
}
