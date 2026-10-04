import Button from '@mui/material/Button'
import Checkbox from '@mui/material/Checkbox'
import Dialog from '@mui/material/Dialog'
import DialogActions from '@mui/material/DialogActions'
import DialogContent from '@mui/material/DialogContent'
import DialogTitle from '@mui/material/DialogTitle'
import FormControlLabel from '@mui/material/FormControlLabel'
import MenuItem from '@mui/material/MenuItem'
import Stack from '@mui/material/Stack'
import TextField from '@mui/material/TextField'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import type { SignalSpec } from '../api/catalogue'
import type { Panel, PanelType } from '../api/dashboards'
import { GAUGE_SORTS } from '../charts/gaugeSort'

const TYPES: PanelType[] = ['chart', 'gauge', 'alerts', 'status']
const LEVELS = ['ERROR', 'WARN', 'INFO', 'DEBUG', 'TRACE']

interface Props {
  open: boolean
  panel: Panel
  specs: SignalSpec[]
  onClose: () => void
  onSave: (panel: Panel) => void
}

export function PanelDialog({ open, panel, specs, onClose, onSave }: Props) {
  const { t } = useTranslation()
  const [draft, setDraft] = useState<Panel>(panel)
  const [wasOpen, setWasOpen] = useState(open)
  if (open !== wasOpen) {
    setWasOpen(open)
    if (open) setDraft(panel)
  }

  const patch = (next: Partial<Panel>) => setDraft((d) => ({ ...d, ...next }))
  const number = (v: string) => (v === '' ? undefined : Math.max(0, Number(v) || 0))
  const signals = specs.filter((s) =>
    draft.type === 'chart' ? s.kind === 'timeseries' : s.kind === 'gauge',
  )
  const dims = specs.find((s) => s.name === draft.signal)?.dims ?? []
  const needsSignal = draft.type === 'chart' || draft.type === 'gauge'
  const ok = !needsSignal || Boolean(draft.signal)

  return (
    <Dialog open={open} onClose={onClose} fullWidth maxWidth="sm">
      <DialogTitle>{t('dashboard.editPanel')}</DialogTitle>
      <DialogContent>
        <Stack spacing={2} sx={{ mt: 1 }}>
          <TextField
            select
            size="small"
            label={t('dashboard.type')}
            value={draft.type}
            onChange={(e) =>
              patch({ type: e.target.value as PanelType, signal: undefined, group: '', sub: '' })
            }
          >
            {TYPES.map((type) => (
              <MenuItem key={type} value={type}>
                {t(`dashboard.${type}`)}
              </MenuItem>
            ))}
          </TextField>
          <TextField
            size="small"
            label={t('dashboard.title')}
            value={draft.title ?? ''}
            onChange={(e) => patch({ title: e.target.value || undefined })}
          />
          {needsSignal ? (
            <TextField
              select
              size="small"
              label={t('dashboard.signal')}
              value={draft.signal ?? ''}
              onChange={(e) => patch({ signal: e.target.value, group: '', sub: '' })}
            >
              {signals.map((s) => (
                <MenuItem key={s.name} value={s.name}>
                  {s.displayName}
                </MenuItem>
              ))}
            </TextField>
          ) : null}
          {draft.type === 'chart' ? (
            <>
              <TextField
                select
                size="small"
                label={t('dashboard.group')}
                value={draft.group ?? ''}
                onChange={(e) => patch({ group: e.target.value, sub: '' })}
              >
                <MenuItem value="">{t('dashboard.none')}</MenuItem>
                {dims.map((d) => (
                  <MenuItem key={d.name} value={d.name}>
                    {d.displayName}
                  </MenuItem>
                ))}
              </TextField>
              <TextField
                select
                size="small"
                label={t('dashboard.sub')}
                value={draft.sub ?? ''}
                disabled={!draft.group}
                onChange={(e) => patch({ sub: e.target.value })}
              >
                <MenuItem value="">{t('dashboard.none')}</MenuItem>
                {dims
                  .filter((d) => d.name !== draft.group)
                  .map((d) => (
                    <MenuItem key={d.name} value={d.name}>
                      {d.displayName}
                    </MenuItem>
                  ))}
              </TextField>
              <TextField
                size="small"
                label={t('dashboard.filter')}
                value={draft.filter ?? ''}
                disabled={!draft.group}
                helperText={t('dashboard.filterHelp')}
                onChange={(e) => patch({ filter: e.target.value || undefined })}
              />
              <TextField
                size="small"
                label={t('dashboard.subFilter')}
                value={draft.subFilter ?? ''}
                disabled={!draft.sub}
                helperText={t('criteria.filterHelp')}
                onChange={(e) => patch({ subFilter: e.target.value || undefined })}
              />
              <FormControlLabel
                label={t('dashboard.main')}
                disabled={!draft.group}
                control={
                  <Checkbox
                    size="small"
                    checked={draft.main !== false}
                    onChange={(e) => patch({ main: e.target.checked ? undefined : false })}
                  />
                }
              />
              <FormControlLabel
                label={t('dashboard.stacked')}
                disabled={!draft.sub}
                control={
                  <Checkbox
                    size="small"
                    checked={draft.stacked !== false}
                    onChange={(e) => patch({ stacked: e.target.checked ? undefined : false })}
                  />
                }
              />
              <FormControlLabel
                label={t('dashboard.together')}
                disabled={!draft.group}
                control={
                  <Checkbox
                    size="small"
                    checked={draft.together === true}
                    onChange={(e) => patch({ together: e.target.checked || undefined })}
                  />
                }
              />
              <Stack direction="row" spacing={2}>
                <TextField
                  size="small"
                  type="number"
                  label={t('dashboard.top')}
                  placeholder="4"
                  helperText={t('dashboard.topHelp')}
                  value={draft.top ?? ''}
                  onChange={(e) => patch({ top: number(e.target.value) })}
                />
                <TextField
                  size="small"
                  type="number"
                  label={t('dashboard.minutes')}
                  value={draft.minutes ?? ''}
                  onChange={(e) => patch({ minutes: number(e.target.value) })}
                />
                <TextField
                  size="small"
                  type="number"
                  label={t('dashboard.groupMinutes')}
                  placeholder="120"
                  disabled={!draft.group}
                  value={draft.groupMinutes ?? ''}
                  onChange={(e) => patch({ groupMinutes: number(e.target.value) })}
                />
              </Stack>
            </>
          ) : null}
          {draft.type === 'gauge' ? (
            <TextField
              select
              size="small"
              label={t('dashboard.sort')}
              value={draft.sort ?? ''}
              onChange={(e) => patch({ sort: e.target.value || undefined })}
            >
              <MenuItem value="">{t('dashboard.sortDefault')}</MenuItem>
              {GAUGE_SORTS.map((sort) => (
                <MenuItem key={sort} value={sort}>
                  {t(`dashboard.sorts.${sort}`)}
                </MenuItem>
              ))}
            </TextField>
          ) : null}
          {draft.type === 'alerts' ? (
            <>
              <Stack direction="row" flexWrap="wrap" useFlexGap>
                {LEVELS.map((level) => (
                  <FormControlLabel
                    key={level}
                    label={level}
                    control={
                      <Checkbox
                        size="small"
                        checked={(draft.levels ?? []).includes(level)}
                        onChange={(e) => {
                          const have = draft.levels ?? []
                          patch({
                            levels: e.target.checked
                              ? [...have, level]
                              : have.filter((l) => l !== level),
                          })
                        }}
                      />
                    }
                  />
                ))}
              </Stack>
              <TextField
                size="small"
                label={t('dashboard.categories')}
                value={(draft.categories ?? []).join(', ')}
                onChange={(e) =>
                  patch({
                    categories: e.target.value
                      .split(',')
                      .map((c) => c.trim())
                      .filter(Boolean),
                  })
                }
              />
              <TextField
                size="small"
                type="number"
                label={t('dashboard.limit')}
                value={draft.limit ?? ''}
                onChange={(e) => patch({ limit: number(e.target.value) })}
              />
            </>
          ) : null}
          {draft.type !== 'status' ? (
            <TextField
              size="small"
              type="number"
              label={t('dashboard.height')}
              value={draft.height ?? ''}
              onChange={(e) => patch({ height: number(e.target.value) })}
            />
          ) : null}
        </Stack>
      </DialogContent>
      <DialogActions>
        <Button onClick={onClose}>{t('common.cancel')}</Button>
        <Button variant="contained" disabled={!ok} onClick={() => onSave(draft)}>
          {t('dashboard.ok')}
        </Button>
      </DialogActions>
    </Dialog>
  )
}
