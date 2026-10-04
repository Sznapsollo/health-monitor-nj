import Alert from '@mui/material/Alert'
import Button from '@mui/material/Button'
import Stack from '@mui/material/Stack'
import Table from '@mui/material/Table'
import TableBody from '@mui/material/TableBody'
import TableCell from '@mui/material/TableCell'
import TableHead from '@mui/material/TableHead'
import TableRow from '@mui/material/TableRow'
import Typography from '@mui/material/Typography'
import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Busy } from '../app/Busy'

import type { SignalSpec } from '../api/catalogue'
import { deleteSignal, dismissCandidate, fetchCandidates, type Candidate } from '../api/signals'
import { useMonitorStore } from '../store/useMonitorStore'
import { blankDraft, draftFromCandidate, draftFromSpec, type SignalDraft } from './draft'
import { SampleDialog } from './SampleDialog'
import { SignalEditor } from './SignalEditor'

const POLL_MS = 5000

interface Props {
  platform: string
  specs: SignalSpec[]
}

export function SignalsPage({ platform, specs }: Props) {
  const { t } = useTranslation()
  const refreshCatalogue = useMonitorStore((s) => s.refreshCatalogue)
  const [candidates, setCandidates] = useState<Candidate[]>([])
  const [loading, setLoading] = useState(true)
  const [draft, setDraft] = useState<SignalDraft | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [done, setDone] = useState<string | null>(null)
  const [showing, setShowing] = useState<Candidate | null>(null)

  const load = useCallback(async (signal?: AbortSignal) => {
    try {
      setCandidates(await fetchCandidates(signal))
      setError(null)
    } catch (err) {
      if (signal?.aborted) return
      setError(err instanceof Error ? err.message : 'unknown error')
    }
    if (!signal?.aborted) setLoading(false)
  }, [])

  useEffect(() => {
    const abort = new AbortController()
    void load(abort.signal)
    const timer = setInterval(() => void load(abort.signal), POLL_MS)
    return () => {
      abort.abort()
      clearInterval(timer)
    }
  }, [load])

  const candidate = draft
    ? candidates.find((c) => c.platform === draft.platform && c.signal === draft.name)
    : undefined

  const saved = async (name: string) => {
    setDraft(null)
    setDone(t('designer.saved', { name }))
    await refreshCatalogue()
    await load()
  }

  const remove = async (spec: SignalSpec) => {
    if (!globalThis.confirm(t('designer.confirmDelete', { name: spec.name }))) return
    setError(null)
    try {
      await deleteSignal(platform, spec.name)
      setDone(t('designer.deleted', { name: spec.name }))
      await refreshCatalogue()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'unknown error')
    }
  }

  const dismiss = async (c: Candidate) => {
    const ask = c.kind === 'log' ? 'designer.confirmRemoveLog' : 'designer.confirmRemove'
    if (!globalThis.confirm(t(ask, { name: c.signal }))) return
    setError(null)
    try {
      await dismissCandidate(c)
      setDone(t('designer.removed', { name: c.signal }))
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'unknown error')
    }
  }

  if (draft) {
    return (
      <SignalEditor
        key={`${draft.platform}/${draft.name}/${draft.isNew}`}
        draft={draft}
        candidate={candidate}
        onCancel={() => setDraft(null)}
        onSaved={(name) => void saved(name)}
      />
    )
  }

  return (
    <Stack spacing={3}>
      {showing ? <SampleDialog candidate={showing} onClose={() => setShowing(null)} /> : null}
      {error ? <Alert severity="error">{error}</Alert> : null}
      {done ? (
        <Alert severity="success" onClose={() => setDone(null)}>
          {done}
        </Alert>
      ) : null}

      <Stack spacing={1}>
        <Typography variant="h6" component="h2">
          {t('designer.waiting')}
        </Typography>
        <Typography variant="body2" color="text.secondary">
          {t('designer.waitingHelp')}
        </Typography>
        {loading ? (
          <Busy />
        ) : candidates.length === 0 ? (
          <Typography variant="body2">{t('designer.noneWaiting')}</Typography>
        ) : (
          <Table size="small">
            <TableHead>
              <TableRow>
                <TableCell align="right" sx={{ width: 48 }}>
                  {t('grid.number')}
                </TableCell>
                <TableCell>{t('designer.platform')}</TableCell>
                <TableCell>{t('designer.signal')}</TableCell>
                <TableCell>{t('designer.kind')}</TableCell>
                <TableCell>{t('designer.packets')}</TableCell>
                <TableCell>{t('designer.lastSeen')}</TableCell>
                <TableCell>{t('designer.fields')}</TableCell>
                <TableCell />
              </TableRow>
            </TableHead>
            <TableBody>
              {candidates.map((c, n) => (
                <TableRow key={`${c.platform}/${c.signal}`}>
                  <TableCell align="right" sx={{ color: 'text.secondary' }}>
                    {n + 1}
                  </TableCell>
                  <TableCell>{c.platform}</TableCell>
                  <TableCell>{c.signal}</TableCell>
                  <TableCell>{c.kind}</TableCell>
                  <TableCell>{c.kind === 'gauge' || c.kind === 'log' ? '—' : c.count}</TableCell>
                  <TableCell>
                    {c.kind === 'log'
                      ? new Date(c.last).toLocaleDateString()
                      : new Date(c.last).toLocaleTimeString()}
                  </TableCell>
                  <TableCell>
                    {c.kind === 'log'
                      ? t('designer.logStored')
                      : c.kind === 'info'
                        ? t('designer.infoWaiting', { fields: Object.keys(c.dims).join(', ') })
                        : [...Object.keys(c.dims), ...Object.keys(c.values)].join(', ')}
                  </TableCell>
                  <TableCell sx={{ whiteSpace: 'nowrap' }}>
                    <Button size="small" onClick={() => setShowing(c)}>
                      {t('designer.showSample')}
                    </Button>
                    <Button size="small" onClick={() => setDraft(draftFromCandidate(c))}>
                      {t('designer.define')}
                    </Button>
                    <Button size="small" color="error" onClick={() => void dismiss(c)}>
                      {t('designer.remove')}
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </Stack>

      <Stack spacing={1}>
        <Stack direction="row" spacing={2} alignItems="center">
          <Typography variant="h6" component="h2">
            {t('designer.defined', { platform })}
          </Typography>
          {platform ? (
            <Button size="small" onClick={() => setDraft(blankDraft(platform))}>
              {t('designer.new')}
            </Button>
          ) : null}
        </Stack>
        <Table size="small">
          <TableHead>
            <TableRow>
              <TableCell align="right" sx={{ width: 48 }}>
                {t('grid.number')}
              </TableCell>
              <TableCell>{t('designer.signal')}</TableCell>
              <TableCell>{t('designer.displayName')}</TableCell>
              <TableCell>{t('designer.kind')}</TableCell>
              <TableCell>{t('designer.fields')}</TableCell>
              <TableCell>{t('designer.origin')}</TableCell>
              <TableCell />
            </TableRow>
          </TableHead>
          <TableBody>
            {specs.map((spec, n) => (
              <TableRow key={spec.name}>
                <TableCell align="right" sx={{ color: 'text.secondary' }}>
                  {n + 1}
                </TableCell>
                <TableCell>{spec.name}</TableCell>
                <TableCell>{spec.displayName}</TableCell>
                <TableCell>{spec.kind}</TableCell>
                <TableCell>{spec.dims.map((d) => d.name).join(', ')}</TableCell>
                <TableCell>
                  {spec.builtIn
                    ? t('designer.builtIn')
                    : spec.readOnly
                      ? t('designer.shipped')
                      : spec.autoRegistered
                        ? t('designer.autoRegistered')
                        : t('designer.by', { name: spec.createdBy ?? '?' })}
                </TableCell>
                <TableCell>
                  {!spec.readOnly ? (
                    <Stack direction="row" spacing={1}>
                      <Button size="small" onClick={() => setDraft(draftFromSpec(platform, spec))}>
                        {t('designer.edit')}
                      </Button>
                      {!spec.autoRegistered ? (
                        <Button size="small" color="error" onClick={() => void remove(spec)}>
                          {t('designer.delete')}
                        </Button>
                      ) : null}
                    </Stack>
                  ) : null}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </Stack>
    </Stack>
  )
}
