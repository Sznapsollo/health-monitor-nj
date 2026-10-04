import Alert from '@mui/material/Alert'
import Button from '@mui/material/Button'
import Dialog from '@mui/material/Dialog'
import DialogActions from '@mui/material/DialogActions'
import DialogContent from '@mui/material/DialogContent'
import DialogTitle from '@mui/material/DialogTitle'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { fetchCandidateSample, type Candidate } from '../api/signals'
import { Busy } from '../app/Busy'
import { JsonTree } from '../info/JsonTree'

interface Props {
  candidate: Candidate
  onClose: () => void
}

/** The last packet an undefined signal arrived with, as a tree. */
export function SampleDialog({ candidate, onClose }: Props) {
  const { t } = useTranslation()
  const [sample, setSample] = useState<unknown>(undefined)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    fetchCandidateSample(candidate)
      .then((s) => !cancelled && setSample(s))
      .catch((err: unknown) => {
        if (!cancelled) setError(err instanceof Error ? err.message : 'unknown error')
      })
    return () => {
      cancelled = true
    }
  }, [candidate])

  return (
    <Dialog open onClose={onClose} fullWidth maxWidth="md">
      <DialogTitle>
        {t(`designer.sampleTitle_${candidate.kind}`, { name: candidate.signal })}
      </DialogTitle>
      <DialogContent>
        {error ? (
          <Alert severity="info">{error}</Alert>
        ) : sample === undefined ? (
          <Busy />
        ) : (
          <JsonTree value={sample} search="" />
        )}
      </DialogContent>
      <DialogActions>
        <Button onClick={onClose}>{t('common.close')}</Button>
      </DialogActions>
    </Dialog>
  )
}
