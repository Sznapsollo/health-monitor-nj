import Alert from '@mui/material/Alert'
import Button from '@mui/material/Button'
import Dialog from '@mui/material/Dialog'
import DialogActions from '@mui/material/DialogActions'
import DialogContent from '@mui/material/DialogContent'
import DialogTitle from '@mui/material/DialogTitle'
import FormControl from '@mui/material/FormControl'
import FormControlLabel from '@mui/material/FormControlLabel'
import InputLabel from '@mui/material/InputLabel'
import MenuItem from '@mui/material/MenuItem'
import Radio from '@mui/material/Radio'
import RadioGroup from '@mui/material/RadioGroup'
import Select from '@mui/material/Select'
import Stack from '@mui/material/Stack'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { createSilence, updateSilence, type Silence } from '../api/alerts'
import { categoryOf, categoryTarget, matchTarget, messageOf } from './silenceTargets'

interface Props {
  open: boolean
  platform: string
  /** The message to start from; it can be cut down to any part. */
  message: string
  /** The category to offer first. */
  category: string
  categories: string[]
  /** A silence to edit instead of making a new one. */
  editing?: Silence
  onClose: () => void
  onCreated: () => void
}

const QUICK_MINUTES = [60, 240, 720, 10080]

const REASONS = [
  "don't need to see it",
  'maintenance',
  'decommissioned',
  'known issue',
  'release in progress',
]

type Scope = 'message' | 'category'

export function SilenceDialog({
  open,
  platform,
  message,
  category,
  categories,
  editing,
  onClose,
  onCreated,
}: Props) {
  const { t } = useTranslation()
  const editedCategory = editing ? categoryOf(editing.target) : null
  const [scope, setScope] = useState<Scope>(editedCategory !== null ? 'category' : 'message')
  const [text, setText] = useState(editing ? (messageOf(editing.target) ?? '') : message)
  const [picked, setPicked] = useState(editedCategory ?? category)
  const [mute, setMute] = useState(editing ? editing.kind === 'mute' : true)
  const [minutes, setMinutes] = useState(60)
  const [reason, setReason] = useState(editing?.reason ?? REASONS[0])
  const [by, setBy] = useState(editing?.by ?? '')
  const [error, setError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)

  const choices = [...new Set([editedCategory ?? category, ...categories].filter((c) => c !== ''))]
  const target = scope === 'message' ? matchTarget(text.trim()) : categoryTarget(picked)
  const complete = scope === 'message' ? text.trim() !== '' : picked !== ''

  const submit = async () => {
    setSaving(true)
    setError(null)
    try {
      const body = {
        target,
        kind: mute ? ('mute' as const) : ('snooze' as const),
        reason: reason.trim(),
        by: by.trim(),
        minutes: mute ? undefined : minutes,
      }
      if (editing) await updateSilence(editing.id, body)
      else await createSilence({ platform, ...body })
      onCreated()
      onClose()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'unknown error')
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog open={open} onClose={onClose} fullWidth maxWidth="sm">
      <DialogTitle>{t(editing ? 'silence.editTitle' : 'silence.title')}</DialogTitle>
      <DialogContent>
        <Stack spacing={2} sx={{ mt: 1 }}>
          <RadioGroup value={scope} onChange={(e) => setScope(e.target.value as Scope)}>
            <FormControlLabel
              value="message"
              control={<Radio size="small" />}
              label={t('silence.byMessage')}
            />
            {scope === 'message' ? (
              <TextField
                size="small"
                autoFocus
                sx={{ ml: 4, mb: 1 }}
                label={t('silence.matchText')}
                value={text}
                onChange={(e) => setText(e.target.value)}
                helperText={t('silence.matchHelp')}
              />
            ) : null}
            <FormControlLabel
              value="category"
              control={<Radio size="small" />}
              label={t('silence.byCategory')}
              disabled={choices.length === 0}
            />
            {scope === 'category' ? (
              <FormControl size="small" sx={{ ml: 4 }}>
                <InputLabel id="silence-category">{t('silence.category')}</InputLabel>
                <Select
                  labelId="silence-category"
                  label={t('silence.category')}
                  value={picked}
                  onChange={(e) => setPicked(String(e.target.value))}
                >
                  {choices.map((c) => (
                    <MenuItem key={c} value={c}>
                      {c}
                    </MenuItem>
                  ))}
                </Select>
              </FormControl>
            ) : null}
          </RadioGroup>

          <Typography variant="body2">{t('silence.effect')}</Typography>

          <FormControl size="small" fullWidth>
            <InputLabel id="silence-kind">{t('silence.kind')}</InputLabel>
            <Select
              labelId="silence-kind"
              label={t('silence.kind')}
              value={mute ? 'mute' : 'snooze'}
              onChange={(e) => setMute(e.target.value === 'mute')}
            >
              <MenuItem value="mute">{t('silence.mute')}</MenuItem>
              <MenuItem value="snooze">{t('silence.snooze')}</MenuItem>
            </Select>
          </FormControl>

          {mute ? null : (
            <FormControl size="small" fullWidth>
              <InputLabel id="silence-for">
                {t(editing ? 'silence.forFromNow' : 'silence.for')}
              </InputLabel>
              <Select
                labelId="silence-for"
                label={t(editing ? 'silence.forFromNow' : 'silence.for')}
                value={minutes}
                onChange={(e) => setMinutes(Number(e.target.value))}
              >
                {QUICK_MINUTES.map((m) => (
                  <MenuItem key={m} value={m}>
                    {t('silence.minutes', { count: m })}
                  </MenuItem>
                ))}
              </Select>
            </FormControl>
          )}

          <FormControl size="small" fullWidth>
            <InputLabel id="silence-reason">{t('silence.reason')}</InputLabel>
            <Select
              labelId="silence-reason"
              label={t('silence.reason')}
              value={REASONS.includes(reason) ? reason : ''}
              onChange={(e) => setReason(String(e.target.value))}
            >
              {REASONS.map((r) => (
                <MenuItem key={r} value={r}>
                  {t(`silence.reasons.${r.replace(/'/g, '').replace(/\s/g, '_')}`)}
                </MenuItem>
              ))}
            </Select>
          </FormControl>

          <TextField
            size="small"
            label={t('silence.reasonText')}
            value={reason}
            onChange={(e) => setReason(e.target.value)}
            helperText={t('silence.reasonHelp')}
            required
          />
          <TextField
            size="small"
            label={t('silence.by')}
            value={by}
            onChange={(e) => setBy(e.target.value)}
            helperText={t('silence.byHelp')}
          />

          {error ? <Alert severity="error">{error}</Alert> : null}
        </Stack>
      </DialogContent>
      <DialogActions>
        <Button onClick={onClose}>{t('common.cancel')}</Button>
        <Button
          variant="contained"
          disabled={!reason.trim() || !complete || saving}
          onClick={() => void submit()}
        >
          {t(editing ? 'silence.save' : 'silence.create')}
        </Button>
      </DialogActions>
    </Dialog>
  )
}
