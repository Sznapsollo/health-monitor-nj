import Checkbox from '@mui/material/Checkbox'
import FormControlLabel from '@mui/material/FormControlLabel'
import MenuItem from '@mui/material/MenuItem'
import Stack from '@mui/material/Stack'
import TextField from '@mui/material/TextField'
import { useTranslation } from 'react-i18next'

import type { DisplayOptions } from '../api/session'

/** What a wall display shows besides its dashboard, and in which colours. */
export function DisplayOptionsEditor({
  value,
  onChange,
}: {
  value: DisplayOptions
  onChange: (next: DisplayOptions) => void
}) {
  const { t } = useTranslation()
  return (
    <Stack direction="row" spacing={1} alignItems="center" flexWrap="wrap" useFlexGap>
      <FormControlLabel
        control={
          <Checkbox
            size="small"
            checked={Boolean(value.showSystem)}
            onChange={(e) => onChange({ ...value, showSystem: e.target.checked })}
          />
        }
        label={t('settings.displaySystem')}
      />
      <FormControlLabel
        control={
          <Checkbox
            size="small"
            checked={Boolean(value.showLive)}
            onChange={(e) => onChange({ ...value, showLive: e.target.checked })}
          />
        }
        label={t('settings.displayLive')}
      />
      <TextField
        select
        size="small"
        label={t('settings.displayTheme')}
        value={value.theme ?? 'system'}
        onChange={(e) => {
          const theme = e.target.value
          onChange({
            ...value,
            theme: theme === 'light' || theme === 'dark' || theme === 'screen' ? theme : undefined,
          })
        }}
        sx={{ minWidth: 190 }}
      >
        <MenuItem value="system">{t('settings.theme_system')}</MenuItem>
        <MenuItem value="light">{t('settings.theme_light')}</MenuItem>
        <MenuItem value="dark">{t('settings.theme_dark')}</MenuItem>
        <MenuItem value="screen">{t('settings.theme_screen')}</MenuItem>
      </TextField>
    </Stack>
  )
}
