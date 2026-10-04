import Chip from '@mui/material/Chip'
import Stack from '@mui/material/Stack'
import Tooltip from '@mui/material/Tooltip'
import Typography from '@mui/material/Typography'
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { useMonitorStore } from '../store/useMonitorStore'
import type { ConnectionState } from '../ws/client'

const COLOR: Record<ConnectionState, 'success' | 'warning' | 'error'> = {
  open: 'success',
  connecting: 'warning',
  closed: 'error',
}

interface Props {
  state: ConnectionState
  /** When the server last sent anything, from the live stream. */
  serverTime: string | null
}

/**
 * Connection state plus how long ago the last message arrived. A monitor whose
 * own charts have quietly stopped updating is worse than useless, so it says
 * out loud when it last heard anything.
 */
export function ConnectionBadge({ state, serverTime }: Props) {
  const { t, i18n } = useTranslation()
  const [now, setNow] = useState(() => Date.now())

  useEffect(() => {
    const handle = globalThis.setInterval(() => setNow(Date.now()), 1000)
    return () => globalThis.clearInterval(handle)
  }, [])

  const clock = useMemo(
    () => new Intl.DateTimeFormat(i18n.language, { timeStyle: 'medium' }),
    [i18n.language],
  )

  const age = serverTime ? Math.round((now - new Date(serverTime).getTime()) / 1000) : null
  const stale = age !== null && age > 30

  return (
    <Stack direction="row" spacing={1} alignItems="center">
      {serverTime ? (
        <Tooltip title={t('connection.lastUpdateAt', { at: clock.format(new Date(serverTime)) })}>
          <Typography
            variant="caption"
            color={stale ? 'warning.main' : 'text.secondary'}
            sx={{
              display: 'inline-block',
              textAlign: 'right',
              fontVariantNumeric: 'tabular-nums',
              minWidth: `${t('connection.lastUpdate', { seconds: 999 }).length}ch`,
            }}
          >
            {t('connection.lastUpdate', { seconds: age ?? 0 })}
          </Typography>
        </Tooltip>
      ) : null}
      <Chip size="small" color={COLOR[state]} label={t(`connection.${state}`)} />
    </Stack>
  )
}

/** The badge fed straight from the store, so its once-a-message updates stay here. */
export function LiveConnectionBadge() {
  const state = useMonitorStore((s) => s.connection)
  const serverTime = useMonitorStore((s) => s.serverTime)
  return <ConnectionBadge state={state} serverTime={serverTime} />
}
