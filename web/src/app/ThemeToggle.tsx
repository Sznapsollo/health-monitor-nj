import BrightnessAutoIcon from '@mui/icons-material/BrightnessAuto'
import DarkModeIcon from '@mui/icons-material/DarkMode'
import LightModeIcon from '@mui/icons-material/LightMode'
import ToggleButton from '@mui/material/ToggleButton'
import ToggleButtonGroup from '@mui/material/ToggleButtonGroup'
import Tooltip from '@mui/material/Tooltip'
import { useTranslation } from 'react-i18next'

import type { ThemeMode } from '../theme/tokens'
import { useThemeMode } from '../theme/useThemeMode'

const MODES: { mode: ThemeMode; icon: typeof LightModeIcon; key: string }[] = [
  { mode: 'light', icon: LightModeIcon, key: 'settings.theme_light' },
  { mode: 'dark', icon: DarkModeIcon, key: 'settings.theme_dark' },
  { mode: 'system', icon: BrightnessAutoIcon, key: 'settings.theme_system' },
]

/**
 * Light, dark or follow the system, one click away in the top bar.
 * "System" is a real choice, not the absence of one: a screen that follows the
 * room it is in should not be indistinguishable from a screen someone set to
 * light at noon.
 */
export function ThemeToggle({ onPick }: { onPick?: (mode: ThemeMode) => void } = {}) {
  const { t } = useTranslation()
  const { mode, setMode } = useThemeMode()

  return (
    <ToggleButtonGroup
      size="small"
      exclusive
      value={mode}
      onChange={(_, next: ThemeMode | null) => {
        // Null means the active button was clicked again; keep the choice.
        if (next === null) return
        setMode(next)
        onPick?.(next)
      }}
      aria-label={t('settings.theme')}
    >
      {MODES.map(({ mode: value, icon: Icon, key }) => (
        <Tooltip key={value} title={t(key)}>
          <ToggleButton value={value} aria-label={t(key)} sx={{ px: 1, border: 0 }}>
            <Icon fontSize="small" />
          </ToggleButton>
        </Tooltip>
      ))}
    </ToggleButtonGroup>
  )
}
