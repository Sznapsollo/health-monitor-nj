import AddIcon from '@mui/icons-material/Add'
import DeleteIcon from '@mui/icons-material/Delete'
import Alert from '@mui/material/Alert'
import Autocomplete from '@mui/material/Autocomplete'
import Button from '@mui/material/Button'
import Checkbox from '@mui/material/Checkbox'
import FormControlLabel from '@mui/material/FormControlLabel'
import IconButton from '@mui/material/IconButton'
import MenuItem from '@mui/material/MenuItem'
import Paper from '@mui/material/Paper'
import Stack from '@mui/material/Stack'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { fetchCandidateSample, fetchLogRetention, saveSignal, type Candidate } from '../api/signals'
import { MinuteSeriesChart } from '../charts/MinuteSeriesChart'
import type { SeriesSpec } from '../charts/options'
import { JsonTree } from '../info/JsonTree'
import { editOf, previewPoints, problemOf, type DimDraft, type SignalDraft } from './draft'

interface Props {
  draft: SignalDraft
  candidate?: Candidate
  onSaved: (name: string) => void
  onCancel: () => void
}

export function SignalEditor({ draft: initial, candidate, onSaved, onCancel }: Props) {
  const { t } = useTranslation()
  const [draft, setDraft] = useState<SignalDraft>(initial)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const [configured, setConfigured] = useState<{ dbDays: number; archiveDays: number } | null>(null)
  const logName = draft.kind === 'log' ? draft.name : ''
  useEffect(() => {
    if (!logName) return
    let cancelled = false
    fetchLogRetention(logName)
      .then((r) => !cancelled && setConfigured(r))
      .catch(() => !cancelled && setConfigured(null))
    return () => {
      cancelled = true
    }
  }, [logName])

  const [sample, setSample] = useState<unknown>(undefined)
  useEffect(() => {
    if (!candidate) return
    let cancelled = false
    fetchCandidateSample(candidate)
      .then((s) => !cancelled && setSample(s))
      .catch(() => !cancelled && setSample(undefined))
    return () => {
      cancelled = true
    }
  }, [candidate])

  const patch = (p: Partial<SignalDraft>) => setDraft((d) => ({ ...d, ...p }))
  const setColor = (i: number, row: [string, string]) =>
    patch({ colors: draft.colors.map((c, k) => (k === i ? row : c)) })
  const sampleValues = useMemo(
    () => [...new Set(draft.dims.filter((d) => d.include).flatMap((d) => d.samples))].sort(),
    [draft.dims],
  )
  const patchDim = (i: number, p: Partial<DimDraft>) =>
    patch({ dims: draft.dims.map((dim, k) => (k === i ? { ...dim, ...p } : dim)) })

  const fields = useMemo(() => {
    const seen = new Set(Object.keys(candidate?.values ?? {}))
    for (const f of [draft.countField, draft.msField]) if (f) seen.add(f)
    return [...seen].sort()
  }, [candidate, draft.countField, draft.msField])

  const preview: SeriesSpec[] = useMemo(() => {
    if (!candidate || draft.kind !== 'timeseries') return []
    const points = previewPoints(candidate, draft.countField, draft.msField)
    const out: SeriesSpec[] = [{ name: t('chart.count'), points, value: 'count', style: 'column' }]
    if (draft.msField) {
      out.push({ name: t('chart.latency'), points, value: 'avgMs', style: 'line', secondary: true })
    }
    return out
  }, [candidate, draft.kind, draft.countField, draft.msField, t])

  const save = async () => {
    const problem = problemOf(draft)
    if (problem) {
      setError(t(problem))
      return
    }
    setBusy(true)
    setError(null)
    try {
      await saveSignal(draft.platform, draft.name, editOf(draft))
      onSaved(draft.name)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'unknown error')
    } finally {
      setBusy(false)
    }
  }

  const number = (value: string) => Math.max(0, Math.round(Number(value) || 0))
  const fieldSelect = (
    label: string,
    value: string,
    onChange: (v: string) => void,
    none: string,
  ) => (
    <Autocomplete
      freeSolo
      size="small"
      options={fields}
      value={value}
      onInputChange={(_, v) => onChange(v.trim())}
      sx={{ minWidth: 220 }}
      renderInput={(params) => (
        <TextField {...params} label={label} helperText={value ? undefined : none} />
      )}
    />
  )

  return (
    <Paper variant="outlined" sx={{ p: 2 }}>
      <Stack spacing={2}>
        <Typography variant="h6" component="h2">
          {draft.isNew ? t('designer.newTitle') : t('designer.editTitle', { name: draft.name })}
        </Typography>
        <Typography variant="body2" color="text.secondary">
          {t('designer.platformIs', { platform: draft.platform })}
        </Typography>

        <Stack direction={{ xs: 'column', md: 'row' }} spacing={2}>
          <TextField
            size="small"
            label={t('designer.name')}
            value={draft.name}
            disabled={!draft.isNew || (candidate !== undefined && candidate.kind !== 'info')}
            onChange={(e) => patch({ name: e.target.value.trim() })}
            helperText={t('designer.nameHelp')}
          />
          <TextField
            select
            size="small"
            label={t('designer.kind')}
            value={draft.kind}
            onChange={(e) => patch({ kind: e.target.value as SignalDraft['kind'] })}
            sx={{ minWidth: 180 }}
          >
            <MenuItem value="timeseries">{t('designer.kindTimeseries')}</MenuItem>
            <MenuItem value="gauge">{t('designer.kindGauge')}</MenuItem>
            <MenuItem value="log">{t('designer.kindLog')}</MenuItem>
            <MenuItem value="info">{t('designer.kindInfo')}</MenuItem>
          </TextField>
          <TextField
            size="small"
            label={t('designer.displayName')}
            value={draft.displayName}
            onChange={(e) => patch({ displayName: e.target.value })}
          />
        </Stack>

        {draft.kind === 'info' ? (
          <>
            <Typography variant="body2" color="text.secondary">
              {t('designer.infoHelp')}
            </Typography>
            <TextField
              size="small"
              label={t('designer.packetType')}
              helperText={t('designer.packetTypeHelp')}
              value={draft.packetType}
              onChange={(e) => patch({ packetType: e.target.value })}
            />
            <FormControlLabel
              control={
                <Checkbox
                  checked={draft.merge}
                  onChange={(e) => patch({ merge: e.target.checked })}
                />
              }
              label={t('designer.merge')}
            />
            <Typography variant="body2" color="text.secondary">
              {t('designer.mergeHelp')}
            </Typography>
            <FormControlLabel
              control={
                <Checkbox
                  checked={!draft.noStatus}
                  onChange={(e) => patch({ noStatus: !e.target.checked })}
                />
              }
              label={t('designer.onStatus')}
            />
            <Typography variant="body2" color="text.secondary">
              {t('designer.onStatusHelp')}
            </Typography>
            <Typography variant="subtitle1">{t('designer.retention')}</Typography>
            <Stack direction={{ xs: 'column', md: 'row' }} spacing={2}>
              {draft.merge ? null : (
                <TextField
                  size="small"
                  type="number"
                  label={t('designer.versions')}
                  helperText={t('designer.versionsHelp')}
                  value={draft.versions}
                  onChange={(e) => patch({ versions: number(e.target.value) })}
                />
              )}
              <TextField
                size="small"
                type="number"
                label={t('designer.durableDays')}
                helperText={t('designer.infoDaysHelp')}
                value={draft.durableDays}
                onChange={(e) => patch({ durableDays: number(e.target.value) })}
              />
            </Stack>
          </>
        ) : draft.kind === 'log' ? (
          <>
            <Typography variant="subtitle1">{t('designer.logRetention')}</Typography>
            <Typography variant="body2" color="text.secondary">
              {t('designer.logRetentionHelp')}
            </Typography>
            <Stack direction={{ xs: 'column', md: 'row' }} spacing={2}>
              <TextField
                size="small"
                type="number"
                label={t('designer.logDays')}
                helperText={
                  configured
                    ? t('designer.logDaysDefault', { count: configured.dbDays })
                    : t('designer.fromConfig')
                }
                value={draft.logDays}
                onChange={(e) => patch({ logDays: e.target.value })}
              />
              <TextField
                size="small"
                type="number"
                label={t('designer.archiveDays')}
                helperText={
                  !configured
                    ? t('designer.archiveDaysHelp')
                    : configured.archiveDays > 0
                      ? t('designer.archiveDaysDefault', { count: configured.archiveDays })
                      : t('designer.archiveDaysDefaultDrop')
                }
                value={draft.archiveDays}
                onChange={(e) => patch({ archiveDays: e.target.value })}
              />
            </Stack>
          </>
        ) : draft.kind === 'timeseries' ? (
          <>
            <TextField
              size="small"
              label={t('designer.packetType')}
              helperText={t('designer.metricPacketTypeHelp')}
              value={draft.packetType}
              onChange={(e) => patch({ packetType: e.target.value })}
              sx={{ maxWidth: 420 }}
            />
            <Typography variant="subtitle1">{t('designer.dims')}</Typography>
            <Typography variant="body2" color="text.secondary">
              {t('designer.dimsHelp')}
            </Typography>
            {draft.dims.map((dim, i) => (
              <Stack
                key={i}
                direction="row"
                spacing={1}
                alignItems="center"
                flexWrap="wrap"
                useFlexGap
              >
                <Checkbox
                  size="small"
                  checked={dim.include}
                  onChange={(e) => patchDim(i, { include: e.target.checked })}
                  slotProps={{
                    input: { 'aria-label': t('designer.includeDim', { name: dim.name }) },
                  }}
                />
                <TextField
                  size="small"
                  label={t('designer.dimName')}
                  value={dim.name}
                  onChange={(e) => patchDim(i, { name: e.target.value.trim() })}
                />
                <TextField
                  size="small"
                  label={t('designer.dimDisplayName')}
                  value={dim.displayName}
                  onChange={(e) => patchDim(i, { displayName: e.target.value })}
                />
                <TextField
                  select
                  size="small"
                  label={t('designer.labelDim')}
                  value={dim.labelDim}
                  onChange={(e) => patchDim(i, { labelDim: e.target.value })}
                  sx={{ minWidth: 180 }}
                >
                  <MenuItem value="">{t('designer.labelNone')}</MenuItem>
                  {draft.dims
                    .filter((d) => d.include && d.name && d.name !== dim.name)
                    .map((d) => (
                      <MenuItem key={d.name} value={d.name}>
                        {d.displayName || d.name}
                      </MenuItem>
                    ))}
                </TextField>
                {dim.samples.length > 0 ? (
                  <Typography
                    variant="caption"
                    color="text.secondary"
                    sx={{ maxWidth: 360 }}
                    noWrap
                  >
                    {t('designer.samples', { values: dim.samples.join(', ') })}
                  </Typography>
                ) : null}
                <IconButton
                  size="small"
                  aria-label={t('designer.removeDim', { name: dim.name })}
                  onClick={() => patch({ dims: draft.dims.filter((_, k) => k !== i) })}
                >
                  <DeleteIcon fontSize="small" />
                </IconButton>
              </Stack>
            ))}
            <Button
              size="small"
              startIcon={<AddIcon />}
              sx={{ alignSelf: 'flex-start' }}
              onClick={() =>
                patch({
                  dims: [
                    ...draft.dims,
                    { name: '', displayName: '', labelDim: '', include: true, samples: [] },
                  ],
                })
              }
            >
              {t('designer.addDim')}
            </Button>

            <Typography variant="subtitle1">{t('designer.values')}</Typography>
            <Stack direction={{ xs: 'column', md: 'row' }} spacing={2}>
              {fieldSelect(
                t('designer.countField'),
                draft.countField,
                (v) => patch({ countField: v }),
                t('designer.countNone'),
              )}
              {fieldSelect(
                t('designer.msField'),
                draft.msField,
                (v) => patch({ msField: v }),
                t('designer.msNone'),
              )}
              <TextField
                size="small"
                type="number"
                label={t('designer.groupTop')}
                value={draft.groupTop}
                onChange={(e) => patch({ groupTop: number(e.target.value) })}
              />
            </Stack>

            <Typography variant="subtitle1">{t('designer.colors')}</Typography>
            <Typography variant="body2" color="text.secondary">
              {t('designer.colorsHelp')}
            </Typography>
            {draft.colors.map(([value, color], i) => (
              <Stack key={i} direction="row" spacing={1} alignItems="center">
                <Autocomplete
                  freeSolo
                  size="small"
                  options={sampleValues}
                  value={value}
                  onInputChange={(_, v) => setColor(i, [v, color])}
                  sx={{ minWidth: 260 }}
                  renderInput={(params) => (
                    <TextField {...params} label={t('designer.colorValue')} />
                  )}
                />
                <TextField
                  size="small"
                  type="color"
                  label={t('designer.color')}
                  value={color}
                  onChange={(e) => setColor(i, [value, e.target.value])}
                  sx={{ width: 90 }}
                />
                <IconButton
                  size="small"
                  aria-label={t('designer.removeColor', { value })}
                  onClick={() => patch({ colors: draft.colors.filter((_, k) => k !== i) })}
                >
                  <DeleteIcon fontSize="small" />
                </IconButton>
              </Stack>
            ))}
            <Button
              size="small"
              startIcon={<AddIcon />}
              sx={{ alignSelf: 'flex-start' }}
              onClick={() => patch({ colors: [...draft.colors, ['', '#2e7d32']] })}
            >
              {t('designer.addColor')}
            </Button>

            <Typography variant="subtitle1">{t('designer.retention')}</Typography>
            <Stack direction={{ xs: 'column', md: 'row' }} spacing={2}>
              <TextField
                size="small"
                type="number"
                label={t('designer.hotDetail')}
                value={draft.hotDetailMinutes}
                onChange={(e) => patch({ hotDetailMinutes: number(e.target.value) })}
              />
              <TextField
                size="small"
                type="number"
                label={t('designer.hotTotals')}
                value={draft.hotTotalsMinutes}
                onChange={(e) => patch({ hotTotalsMinutes: number(e.target.value) })}
              />
              <TextField
                size="small"
                type="number"
                label={t('designer.durableDays')}
                value={draft.durableDays}
                onChange={(e) => patch({ durableDays: number(e.target.value) })}
              />
              <TextField
                size="small"
                type="number"
                label={t('designer.maxKeys')}
                value={draft.maxKeys}
                onChange={(e) => patch({ maxKeys: number(e.target.value) })}
              />
              <TextField
                size="small"
                type="number"
                label={t('designer.detailDays')}
                helperText={t('designer.detailDaysHelp')}
                value={draft.detailDays}
                onChange={(e) => patch({ detailDays: number(e.target.value) })}
              />
            </Stack>

            <Typography variant="subtitle1">{t('designer.keepPairs')}</Typography>
            <Typography variant="body2" color="text.secondary">
              {t('designer.keepPairsHelp')}
            </Typography>
            {draft.keepPairs.map(([main, sub], i) => (
              <Stack key={i} direction="row" spacing={1} alignItems="center">
                {([main, sub] as const).map((value, side) => (
                  <TextField
                    key={side}
                    select
                    size="small"
                    sx={{ minWidth: 180 }}
                    label={side === 0 ? t('designer.pairMain') : t('designer.pairSub')}
                    value={value}
                    onChange={(e) => {
                      const next = [...draft.keepPairs]
                      const pair: [string, string] = [...next[i]!]
                      pair[side] = e.target.value
                      next[i] = pair
                      patch({ keepPairs: next })
                    }}
                  >
                    {draft.dims
                      .filter((d) => d.include)
                      .map((d) => (
                        <MenuItem key={d.name} value={d.name}>
                          {d.displayName || d.name}
                        </MenuItem>
                      ))}
                  </TextField>
                ))}
                <IconButton
                  size="small"
                  aria-label={t('designer.removePair')}
                  onClick={() => patch({ keepPairs: draft.keepPairs.filter((_, k) => k !== i) })}
                >
                  <DeleteIcon fontSize="small" />
                </IconButton>
              </Stack>
            ))}
            <Button
              size="small"
              startIcon={<AddIcon />}
              sx={{ alignSelf: 'flex-start' }}
              disabled={draft.dims.filter((d) => d.include).length < 2}
              onClick={() => {
                const included = draft.dims.filter((d) => d.include).map((d) => d.name)
                patch({ keepPairs: [...draft.keepPairs, [included[0]!, included[1]!]] })
              }}
            >
              {t('designer.addPair')}
            </Button>
          </>
        ) : (
          <TextField
            size="small"
            type="number"
            label={t('designer.ttl')}
            helperText={t('designer.ttlHelp')}
            value={draft.ttlSeconds}
            onChange={(e) => patch({ ttlSeconds: number(e.target.value) })}
            sx={{ maxWidth: 320 }}
          />
        )}

        {candidate &&
        candidate.kind !== 'log' &&
        (candidate.kind !== 'info' || draft.kind === 'timeseries') ? (
          <>
            <Typography variant="subtitle1">{t('designer.preview')}</Typography>
            <Typography variant="body2" color="text.secondary">
              {t('designer.previewHelp', { count: candidate.count })}
            </Typography>
            {preview.length > 0 ? (
              <MinuteSeriesChart series={preview} showLegend height={220} />
            ) : null}
            {candidate.kind === 'gauge' ? (
              <Typography variant="body2">
                {t('designer.gaugePreview', {
                  labels: (candidate.dims.label ?? []).join(', '),
                  min: candidate.values.value?.min ?? 0,
                  max: candidate.values.value?.max ?? 0,
                })}
              </Typography>
            ) : null}
          </>
        ) : null}

        {sample !== undefined ? (
          <>
            <Typography variant="subtitle1">{t('designer.lastPacket')}</Typography>
            <Paper variant="outlined" sx={{ p: 1, maxHeight: 320, overflow: 'auto' }}>
              <JsonTree value={sample} search="" />
            </Paper>
          </>
        ) : null}

        {error ? <Alert severity="error">{error}</Alert> : null}
        <Stack direction="row" spacing={1}>
          <Button variant="contained" disabled={busy} onClick={() => void save()}>
            {t('designer.save')}
          </Button>
          <Button onClick={onCancel}>{t('designer.cancel')}</Button>
        </Stack>
      </Stack>
    </Paper>
  )
}
