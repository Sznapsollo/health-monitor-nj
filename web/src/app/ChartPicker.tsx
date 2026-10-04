import Checkbox from '@mui/material/Checkbox'
import Chip from '@mui/material/Chip'
import Divider from '@mui/material/Divider'
import ListItemText from '@mui/material/ListItemText'
import ListSubheader from '@mui/material/ListSubheader'
import MenuItem from '@mui/material/MenuItem'
import Select from '@mui/material/Select'
import Stack from '@mui/material/Stack'
import { useTranslation } from 'react-i18next'

import type { SignalSpec } from '../api/catalogue'

/** Menu entries that act rather than pick; no signal is named either of these. */
const ALL = '\\all'
const NONE = '\\none'

interface Props {
  /** Every chart and current value this platform defines, in catalogue order. */
  specs: SignalSpec[]
  visible: string[]
  onChange: (signals: string[]) => void
}

/**
 * Which charts and current values the tab draws; nothing until picked. A
 * chart that is not picked is not asked for at all, so the server stops
 * breaking it down and sending it.
 */
export function ChartPicker({ specs, visible, onChange }: Props) {
  const { t } = useTranslation()
  const all = specs.map((s) => s.name)
  const chosen = visible.filter((name) => all.includes(name))

  const pick = (value: string | string[]) => {
    const next = typeof value === 'string' ? value.split(',') : value
    if (next.includes(ALL)) return onChange(all)
    if (next.includes(NONE)) return onChange([])
    onChange(next)
  }

  return (
    <Select
      multiple
      size="small"
      displayEmpty
      value={chosen}
      onChange={(e) => pick(e.target.value)}
      renderValue={() => (
        <Stack direction="row" spacing={1} alignItems="center">
          <span>{t('picker.label')}</span>
          <Chip
            size="small"
            color={chosen.length === all.length ? 'default' : 'primary'}
            label={t('picker.count', { shown: chosen.length, total: all.length })}
          />
        </Stack>
      )}
      sx={{ minWidth: 220 }}
      MenuProps={{ PaperProps: { sx: { maxHeight: 420 } } }}
    >
      {(['timeseries', 'gauge'] as const).flatMap((kind) => {
        const ofKind = specs.filter((s) => s.kind === kind)
        if (ofKind.length === 0) return []
        return [
          <ListSubheader key={kind}>{t(`picker.${kind}`)}</ListSubheader>,
          ...ofKind.map((spec) => (
            <MenuItem key={spec.name} value={spec.name}>
              <Checkbox size="small" checked={chosen.includes(spec.name)} />
              <ListItemText primary={spec.displayName || spec.name} />
            </MenuItem>
          )),
        ]
      })}

      <Divider />
      <MenuItem value={ALL}>{t('picker.all')}</MenuItem>
      <MenuItem value={NONE}>{t('picker.none')}</MenuItem>
    </Select>
  )
}
