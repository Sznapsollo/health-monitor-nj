import AddIcon from '@mui/icons-material/Add'
import DeleteIcon from '@mui/icons-material/Delete'
import ExpandMoreIcon from '@mui/icons-material/ExpandMore'
import ScienceIcon from '@mui/icons-material/Science'
import Accordion from '@mui/material/Accordion'
import AccordionDetails from '@mui/material/AccordionDetails'
import AccordionSummary from '@mui/material/AccordionSummary'
import Alert from '@mui/material/Alert'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Checkbox from '@mui/material/Checkbox'
import IconButton from '@mui/material/IconButton'
import Link from '@mui/material/Link'
import MenuItem from '@mui/material/MenuItem'
import Paper from '@mui/material/Paper'
import Select from '@mui/material/Select'
import Stack from '@mui/material/Stack'
import Table from '@mui/material/Table'
import TableBody from '@mui/material/TableBody'
import TableCell from '@mui/material/TableCell'
import TableHead from '@mui/material/TableHead'
import TableRow from '@mui/material/TableRow'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { SilenceRules } from '../alerts/SilenceRules'
import { Busy } from '../app/Busy'
import { README_URL } from '../app/TabHint'

import {
  fetchRules,
  saveRules,
  testRules,
  type RuleOverride,
  type Rules,
  type RulesTest,
} from '../api/alerts'

const SECTION_BODY = { pl: 3, borderLeft: 2, borderColor: 'divider' }

/** The Alert rules tab: rules that hide alerts, then rules that raise them. */
export function RulesPanel({ platform }: { platform: string }) {
  const { t } = useTranslation()
  return (
    <Stack spacing={5}>
      <Box component="section">
        <Typography variant="h6" component="h2">
          {t('rules.silenceSection')}
        </Typography>
        <Typography variant="body2" color="text.secondary" sx={{ mb: 2 }}>
          {t('rules.silenceSectionHelp')}
        </Typography>
        <Box sx={SECTION_BODY}>
          <SilenceRules platform={platform} />
        </Box>
      </Box>
      <Box component="section">
        <Typography variant="h6" component="h2">
          {t('rules.latencySection')}
        </Typography>
        <Typography variant="body2" color="text.secondary" sx={{ mb: 2 }}>
          {t('rules.latencySectionHelp')}
        </Typography>
        <Box sx={SECTION_BODY}>
          <RaisingRules key={platform} platform={platform} />
        </Box>
      </Box>
    </Stack>
  )
}

/**
 * The thresholds that used to be hard-coded, and "test against the last hour"
 * to see what a change would have done before it goes live.
 */
function RaisingRules({ platform }: { platform: string }) {
  const { t } = useTranslation()
  const [rules, setRules] = useState<Rules | null>(null)
  const [preview, setPreview] = useState<RulesTest | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [saved, setSaved] = useState(false)
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    const controller = new AbortController()
    fetchRules(platform, controller.signal)
      .then(setRules)
      .catch((err: unknown) => {
        if (!controller.signal.aborted) {
          setError(err instanceof Error ? err.message : 'unknown error')
        }
      })
    return () => controller.abort()
  }, [platform])

  if (!rules) {
    return error ? <Alert severity="error">{error}</Alert> : <Busy />
  }

  const overrides = rules.latency.overrides ?? []
  const patchOverride = (index: number, patch: Partial<RuleOverride>) => {
    const next = overrides.map((o, i) => (i === index ? { ...o, ...patch } : o))
    setRules({ ...rules, latency: { ...rules.latency, overrides: next } })
    setSaved(false)
  }

  const run = async (fn: () => Promise<void>) => {
    setBusy(true)
    setError(null)
    try {
      await fn()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'unknown error')
    } finally {
      setBusy(false)
    }
  }

  return (
    <Stack spacing={3}>
      {error ? <Alert severity="error">{error}</Alert> : null}
      {saved ? <Alert severity="success">{t('rules.saved')}</Alert> : null}

      <Accordion disableGutters variant="outlined">
        <AccordionSummary expandIcon={<ExpandMoreIcon />}>
          <Typography variant="subtitle2">{t('rules.howTo')}</Typography>
        </AccordionSummary>
        <AccordionDetails>
          <Box component="ol" sx={{ mt: 0, pl: 3, '& li': { mb: 0.5 } }}>
            {HOW_TO_STEPS.map((step) => (
              <Typography component="li" variant="body2" key={step}>
                {t(`rules.${step}`)}
              </Typography>
            ))}
          </Box>
          <Typography variant="body2" color="text.secondary">
            {t('rules.fileOnly')}{' '}
            <Link
              href={`${README_URL}#writing-alert-rules`}
              target="_blank"
              rel="noopener noreferrer"
            >
              {t('rules.readMore')}
            </Link>
          </Typography>
        </AccordionDetails>
      </Accordion>

      <Stack direction="row" spacing={2} alignItems="center" flexWrap="wrap" useFlexGap>
        <TextField
          size="small"
          type="number"
          label={t('rules.defaultMs')}
          value={rules.latency.defaultMs}
          onChange={(e) => {
            setRules({
              ...rules,
              latency: { ...rules.latency, defaultMs: Number(e.target.value) },
            })
            setSaved(false)
          }}
          helperText={t('rules.defaultMsHelp')}
        />
      </Stack>

      <Box>
        <Stack direction="row" alignItems="center" justifyContent="space-between" sx={{ mb: 1 }}>
          <Typography variant="subtitle1">{t('rules.overrides')}</Typography>
          <Button
            size="small"
            startIcon={<AddIcon />}
            onClick={() => {
              setRules({
                ...rules,
                latency: { ...rules.latency, overrides: [...overrides, { prefix: '', ms: 2000 }] },
              })
              setSaved(false)
            }}
          >
            {t('rules.addOverride')}
          </Button>
        </Stack>

        <Box sx={{ overflowX: 'auto' }}>
          <Table size="small">
            <TableHead>
              <TableRow>
                <TableCell>{t('rules.match')}</TableCell>
                <TableCell>{t('rules.pattern')}</TableCell>
                <TableCell>{t('rules.ms')}</TableCell>
                <TableCell>{t('rules.ignore')}</TableCell>
                <TableCell align="right" />
              </TableRow>
            </TableHead>
            <TableBody>
              {overrides.map((o, i) => (
                <TableRow key={i}>
                  <TableCell>
                    <Select
                      size="small"
                      value={matchKindOf(o)}
                      onChange={(e) => {
                        const kind = String(e.target.value) as 'path' | 'prefix' | 'glob'
                        const value = o.path ?? o.prefix ?? o.glob ?? ''
                        patchOverride(i, {
                          path: undefined,
                          prefix: undefined,
                          glob: undefined,
                          [kind]: value,
                        })
                      }}
                    >
                      <MenuItem value="path">{t('rules.exact')}</MenuItem>
                      <MenuItem value="prefix">{t('rules.prefix')}</MenuItem>
                      <MenuItem value="glob">{t('rules.glob')}</MenuItem>
                    </Select>
                  </TableCell>
                  <TableCell>
                    <TextField
                      size="small"
                      placeholder={PATTERN_EXAMPLES[matchKindOf(o)]}
                      value={o.path ?? o.prefix ?? o.glob ?? ''}
                      onChange={(e) => patchOverride(i, { [matchKindOf(o)]: e.target.value })}
                      sx={{ minWidth: 220 }}
                    />
                  </TableCell>
                  <TableCell>
                    <TextField
                      size="small"
                      type="number"
                      value={o.ms ?? ''}
                      disabled={o.ignore}
                      onChange={(e) => patchOverride(i, { ms: Number(e.target.value) })}
                      sx={{ width: 110 }}
                    />
                  </TableCell>
                  <TableCell>
                    <Checkbox
                      size="small"
                      checked={Boolean(o.ignore)}
                      onChange={(e) => patchOverride(i, { ignore: e.target.checked })}
                    />
                  </TableCell>
                  <TableCell align="right">
                    <IconButton
                      size="small"
                      aria-label={t('rules.removeOverride')}
                      onClick={() => {
                        setRules({
                          ...rules,
                          latency: {
                            ...rules.latency,
                            overrides: overrides.filter((_, j) => j !== i),
                          },
                        })
                        setSaved(false)
                      }}
                    >
                      <DeleteIcon fontSize="small" />
                    </IconButton>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </Box>
      </Box>

      <Box>
        <Typography variant="subtitle1" gutterBottom>
          {t('rules.offlineSection')}
        </Typography>
        <Stack direction="row" spacing={2} flexWrap="wrap" useFlexGap>
          <TextField
            size="small"
            type="number"
            label={t('rules.offlineAfter')}
            value={rules.offline.afterSeconds}
            onChange={(e) => {
              setRules({
                ...rules,
                offline: { ...rules.offline, afterSeconds: Number(e.target.value) },
              })
              setSaved(false)
            }}
            helperText={t('rules.offlineAfterHelp')}
          />
          <TextField
            size="small"
            type="number"
            label={t('rules.remindEvery')}
            value={Math.round((rules.offline.repeatSeconds ?? 300) / 60)}
            slotProps={{ htmlInput: { min: 0 } }}
            onChange={(e) => {
              const minutes = Math.max(0, Math.round(Number(e.target.value) || 0))
              setRules({ ...rules, offline: { ...rules.offline, repeatSeconds: minutes * 60 } })
              setSaved(false)
            }}
            helperText={t('rules.remindEveryHelp')}
          />
        </Stack>
      </Box>

      <Stack direction="row" spacing={1}>
        <Button
          variant="outlined"
          startIcon={<ScienceIcon />}
          disabled={busy}
          onClick={() =>
            void run(async () => {
              setPreview(await testRules(platform, rules))
            })
          }
        >
          {t('rules.test')}
        </Button>
        <Button
          variant="contained"
          disabled={busy}
          onClick={() =>
            void run(async () => {
              setRules(await saveRules(platform, rules))
              setSaved(true)
            })
          }
        >
          {t('rules.save')}
        </Button>
        {busy ? <Busy label={t('common.working')} /> : null}
      </Stack>

      {preview ? (
        <Paper variant="outlined" sx={{ p: 2 }}>
          <Typography variant="subtitle2" gutterBottom>
            {t('rules.previewTitle', { minutes: preview.minutes })}
          </Typography>
          <Typography variant="body2" gutterBottom>
            {t('rules.previewSummary', { total: preview.total, rows: preview.rows })}
          </Typography>
          {(preview.byRule ?? []).map((b) => (
            <Typography key={b.rule} variant="body2" color="text.secondary">
              {t('rules.previewRule', { rule: b.rule, count: b.count })}
            </Typography>
          ))}
        </Paper>
      ) : null}
    </Stack>
  )
}

const HOW_TO_STEPS = ['stepDefault', 'stepAdd', 'stepMatch', 'stepLimit', 'stepTest', 'stepOrder']

const PATTERN_EXAMPLES = {
  path: '/api/orders/export',
  prefix: '/api/reports/',
  glob: '/api/*/search',
} as const

function matchKindOf(o: RuleOverride): 'path' | 'prefix' | 'glob' {
  if (o.path !== undefined) return 'path'
  if (o.glob !== undefined) return 'glob'
  return 'prefix'
}
