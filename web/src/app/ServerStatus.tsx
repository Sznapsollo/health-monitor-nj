import Alert from '@mui/material/Alert'
import Chip from '@mui/material/Chip'
import Stack from '@mui/material/Stack'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { fetchHealth, type Health } from '../api/health'
import { formatElapsed } from './elapsed'

type State = { kind: 'loading' } | { kind: 'ok'; health: Health } | { kind: 'error' }

export function ServerStatus() {
  const { t } = useTranslation()
  const [state, setState] = useState<State>({ kind: 'loading' })

  useEffect(() => {
    const controller = new AbortController()
    fetchHealth(controller.signal)
      .then((health) => setState({ kind: 'ok', health }))
      .catch(() => {
        if (!controller.signal.aborted) setState({ kind: 'error' })
      })
    return () => controller.abort()
  }, [])

  if (state.kind === 'loading') return <Alert severity="info">{t('status.checking')}</Alert>
  if (state.kind === 'error') return <Alert severity="error">{t('status.offline')}</Alert>

  return (
    <Stack direction="row" spacing={1} alignItems="center" flexWrap="wrap" useFlexGap>
      <Alert severity="success">{t('status.online')}</Alert>
      <Chip label={`${t('status.version')}: ${state.health.version}`} size="small" />
      <Chip
        label={`${t('status.uptime')}: ${formatElapsed(state.health.uptimeSeconds)}`}
        size="small"
      />
    </Stack>
  )
}
