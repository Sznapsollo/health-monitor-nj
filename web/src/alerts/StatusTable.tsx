import DeleteForeverIcon from '@mui/icons-material/DeleteForever'
import ManageSearchIcon from '@mui/icons-material/ManageSearch'
import Alert from '@mui/material/Alert'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Chip from '@mui/material/Chip'
import IconButton from '@mui/material/IconButton'
import Stack from '@mui/material/Stack'
import Table from '@mui/material/Table'
import TableBody from '@mui/material/TableBody'
import TableCell from '@mui/material/TableCell'
import TableHead from '@mui/material/TableHead'
import TableRow from '@mui/material/TableRow'
import Tooltip from '@mui/material/Tooltip'
import Typography from '@mui/material/Typography'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Busy } from '../app/Busy'
import { PacketsDialog } from '../app/PacketsDialog'

import { forgetEntity, type StatusEntity } from '../api/alerts'
import { useAlertStore } from '../store/useAlertStore'

/** The old app, jobs and mail server status viewers, as one table. */
interface Props {
  platform: string
  /** Inside a dashboard panel: no counters above the table. */
  compact?: boolean
}

export function StatusTable({ platform, compact = false }: Props) {
  const { t, i18n } = useTranslation()
  const { entities, online, offline, packets, load, loading } = useAlertStore()
  const [error, setError] = useState<string | null>(null)
  const [watching, setWatching] = useState<{ sender?: string } | null>(null)

  const time = useMemo(
    () => new Intl.DateTimeFormat(i18n.language, { dateStyle: 'short', timeStyle: 'medium' }),
    [i18n.language],
  )

  const forget = async (entity: StatusEntity) => {
    setError(null)
    try {
      await forgetEntity({ platform, signal: entity.signal, key: entity.key })
      await load(platform)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'unknown error')
    }
  }

  return (
    <Stack spacing={2}>
      {error ? <Alert severity="error">{error}</Alert> : null}
      {loading && entities.length === 0 ? <Busy /> : null}
      {compact ? null : (
        <Typography variant="body2" color="text.secondary">
          {t('status.help')}
        </Typography>
      )}
      {compact ? null : (
        <Stack direction="row" spacing={1} alignItems="center">
          <Chip size="small" color="success" label={t('status.online', { count: online })} />
          <Chip
            size="small"
            color={offline > 0 ? 'error' : 'default'}
            label={t('status.offline', { count: offline })}
          />
          <Box sx={{ flexGrow: 1 }} />
          <Button size="small" variant="outlined" onClick={() => setWatching({})}>
            {t('packets.open')}
          </Button>
        </Stack>
      )}

      <Box sx={{ overflowX: 'auto' }}>
        <Table size="small">
          <TableHead>
            <TableRow>
              <TableCell align="right" sx={{ width: 48 }}>
                {t('grid.number')}
              </TableCell>
              <TableCell>{t('status.entity')}</TableCell>
              <TableCell>{t('status.state')}</TableCell>
              <TableCell>{t('status.lastSeen')}</TableCell>
              <TableCell align="right">{t('status.packets')}</TableCell>
              <TableCell>{t('status.details')}</TableCell>
              <TableCell align="right" />
            </TableRow>
          </TableHead>
          <TableBody>
            {entities.map((e, n) => {
              const target = `status:${e.signal}/${e.key}`
              return (
                <TableRow key={target}>
                  <TableCell align="right" sx={{ color: 'text.secondary' }}>
                    {n + 1}
                  </TableCell>
                  <TableCell>
                    <Typography variant="body2">{e.key}</Typography>
                    <Typography variant="caption" color="text.secondary">
                      {e.signal}
                    </Typography>
                  </TableCell>
                  <TableCell>
                    <Chip
                      size="small"
                      color={e.offline ? 'error' : 'success'}
                      label={e.offline ? t('status.down') : t('status.up')}
                    />
                  </TableCell>
                  <TableCell sx={{ whiteSpace: 'nowrap' }}>
                    {time.format(new Date(e.lastSeen))}
                  </TableCell>
                  <TableCell align="right">
                    {packets[e.key] !== undefined ? packets[e.key].toLocaleString() : '—'}
                  </TableCell>
                  <TableCell>
                    <Typography variant="caption" color="text.secondary">
                      {describe(e)}
                    </Typography>
                  </TableCell>
                  <TableCell align="right" sx={{ whiteSpace: 'nowrap' }}>
                    {compact ? null : (
                      <Tooltip title={t('packets.fromSender')}>
                        <IconButton
                          size="small"
                          aria-label={t('packets.fromSender')}
                          onClick={() => setWatching({ sender: e.key })}
                        >
                          <ManageSearchIcon fontSize="small" />
                        </IconButton>
                      </Tooltip>
                    )}
                    <Tooltip title={t('status.forget')}>
                      <IconButton
                        size="small"
                        aria-label={t('status.forget')}
                        onClick={() => void forget(e)}
                      >
                        <DeleteForeverIcon fontSize="small" />
                      </IconButton>
                    </Tooltip>
                  </TableCell>
                </TableRow>
              )
            })}
          </TableBody>
        </Table>
      </Box>
      {watching ? (
        <PacketsDialog
          key={watching.sender ?? ''}
          open
          sender={watching.sender}
          onClose={() => setWatching(null)}
        />
      ) : null}
    </Stack>
  )
}

function describe(e: StatusEntity): string {
  const payload = e.payload ?? {}
  return Object.entries(payload)
    .filter(([, v]) => typeof v === 'string' || typeof v === 'number')
    .slice(0, 4)
    .map(([k, v]) => `${k}: ${String(v)}`)
    .join(' · ')
}
