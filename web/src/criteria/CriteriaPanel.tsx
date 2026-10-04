import Checkbox from '@mui/material/Checkbox'
import FormControl from '@mui/material/FormControl'
import FormHelperText from '@mui/material/FormHelperText'
import FormControlLabel from '@mui/material/FormControlLabel'
import InputLabel from '@mui/material/InputLabel'
import MenuItem from '@mui/material/MenuItem'
import Select from '@mui/material/Select'
import Stack from '@mui/material/Stack'
import TextField from '@mui/material/TextField'
import { useTranslation } from 'react-i18next'

import type { SignalSpec } from '../api/catalogue'
import type { Criteria } from '../ws/types'

interface Props {
  spec: SignalSpec
  criteria: Criteria
  onChange: (criteria: Criteria) => void
}

const HISTORY_CHOICES = [5, 15, 30, 60, 120, 180, 360, 720, 1440]

/**
 * The old "Filtry" panel, built entirely from the signal's own definition: the
 * group-by pickers are its declared dimensions, so a new signal needs no form
 * of its own.
 */
export function CriteriaPanel({ spec, criteria, onChange }: Props) {
  const { t } = useTranslation()
  const set = (patch: Partial<Criteria>) => onChange({ ...criteria, ...patch })

  return (
    <Stack spacing={2} sx={{ minWidth: 240 }}>
      <FormControl size="small" fullWidth>
        <InputLabel id={`${spec.name}-history`}>{t('criteria.history')}</InputLabel>
        <Select
          labelId={`${spec.name}-history`}
          label={t('criteria.history')}
          value={criteria.historyMinutes ?? 60}
          onChange={(e) => set({ historyMinutes: Number(e.target.value) })}
        >
          {HISTORY_CHOICES.map((m) => (
            <MenuItem key={m} value={m}>
              {t('criteria.minutes', { count: m })}
            </MenuItem>
          ))}
        </Select>
      </FormControl>

      <FormControl size="small" fullWidth>
        <InputLabel id={`${spec.name}-group`}>{t('criteria.group')}</InputLabel>
        <Select
          labelId={`${spec.name}-group`}
          label={t('criteria.group')}
          value={criteria.group ?? ''}
          onChange={(e) => set({ group: String(e.target.value), sub: '' })}
        >
          <MenuItem value="">{t('criteria.none')}</MenuItem>
          {spec.dims.map((dim) => (
            <MenuItem key={dim.name} value={dim.name}>
              {dim.displayName}
            </MenuItem>
          ))}
        </Select>
      </FormControl>

      <FormControl size="small" fullWidth disabled={!criteria.group}>
        <InputLabel id={`${spec.name}-group-history`}>{t('criteria.groupHistory')}</InputLabel>
        <Select
          labelId={`${spec.name}-group-history`}
          label={t('criteria.groupHistory')}
          value={criteria.groupMinutes ?? 120}
          onChange={(e) => set({ groupMinutes: Number(e.target.value) })}
        >
          {HISTORY_CHOICES.map((m) => (
            <MenuItem key={m} value={m}>
              {t('criteria.minutes', { count: m })}
            </MenuItem>
          ))}
        </Select>
        {criteria.group && (criteria.groupMinutes ?? 120) > spec.retention.hotDetailMinutes ? (
          <FormHelperText>
            {t('criteria.groupHistoryKept', { count: spec.retention.hotDetailMinutes })}
          </FormHelperText>
        ) : null}
      </FormControl>

      <FormControl size="small" fullWidth disabled={!criteria.group}>
        <InputLabel id={`${spec.name}-sub`}>{t('criteria.sub')}</InputLabel>
        <Select
          labelId={`${spec.name}-sub`}
          label={t('criteria.sub')}
          value={criteria.sub ?? ''}
          onChange={(e) => set({ sub: String(e.target.value) })}
        >
          <MenuItem value="">{t('criteria.none')}</MenuItem>
          {spec.dims
            .filter((dim) => dim.name !== criteria.group)
            .map((dim) => (
              <MenuItem key={dim.name} value={dim.name}>
                {dim.displayName}
              </MenuItem>
            ))}
        </Select>
      </FormControl>

      <FormControlLabel
        label={t('criteria.main')}
        disabled={!criteria.group}
        control={
          <Checkbox
            size="small"
            checked={criteria.main !== false}
            onChange={(e) => set({ main: e.target.checked ? undefined : false })}
          />
        }
      />

      <FormControlLabel
        label={t('criteria.stacked')}
        disabled={!criteria.sub}
        control={
          <Checkbox
            size="small"
            checked={criteria.stacked !== false}
            onChange={(e) => set({ stacked: e.target.checked ? undefined : false })}
          />
        }
      />

      <FormControlLabel
        label={t('criteria.together')}
        disabled={!criteria.group}
        control={
          <Checkbox
            size="small"
            checked={criteria.together === true}
            onChange={(e) => set({ together: e.target.checked || undefined })}
          />
        }
      />

      <TextField
        size="small"
        label={t('criteria.filter')}
        value={criteria.groupFilter ?? ''}
        onChange={(e) => set({ groupFilter: e.target.value })}
        helperText={t('criteria.filterHelp')}
        disabled={!criteria.group}
      />

      <TextField
        size="small"
        type="number"
        label={t('criteria.top')}
        value={criteria.groupTop ?? 50}
        onChange={(e) => set({ groupTop: Number(e.target.value) })}
        disabled={!criteria.group}
      />

      <FormControl size="small" fullWidth>
        <InputLabel id={`${spec.name}-sort`}>{t('criteria.sort')}</InputLabel>
        <Select
          labelId={`${spec.name}-sort`}
          label={t('criteria.sort')}
          value={criteria.sortBy ?? 'count'}
          onChange={(e) => set({ sortBy: e.target.value as Criteria['sortBy'] })}
        >
          <MenuItem value="count">{t('chart.count')}</MenuItem>
          <MenuItem value="avgMs">{t('chart.latency')}</MenuItem>
        </Select>
      </FormControl>
    </Stack>
  )
}
