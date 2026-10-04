import Chip from '@mui/material/Chip'
import Stack from '@mui/material/Stack'
import Table from '@mui/material/Table'
import TableBody from '@mui/material/TableBody'
import TableCell from '@mui/material/TableCell'
import TableHead from '@mui/material/TableHead'
import TableRow from '@mui/material/TableRow'
import Typography from '@mui/material/Typography'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Busy } from './Busy'

import { fetchViewers, type ViewerSession } from '../api/session'
import { everyVisible } from './everyVisible'
import { describeUserAgent } from './userAgent'

/** Who has the monitor open right now, and what each is subscribed to. */
export function ViewersTable() {
  const { t } = useTranslation()
  const [viewers, setViewers] = useState<ViewerSession[]>([])
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    const controller = new AbortController()
    const load = () =>
      fetchViewers(controller.signal)
        .then(setViewers)
        .catch(() => {
          if (!controller.signal.aborted) setViewers([])
        })
        .finally(() => {
          if (!controller.signal.aborted) setLoading(false)
        })
    void load()
    const stop = everyVisible(() => void load(), 30_000)
    return () => {
      controller.abort()
      stop()
    }
  }, [])

  const time = (iso: string) => new Date(iso).toLocaleString()
  const hint = (
    <Typography variant="body2" color="text.secondary" sx={{ mb: 1 }}>
      {t('settings.viewersHelp')}
    </Typography>
  )
  if (loading) return <Busy />
  if (viewers.length === 0) {
    return (
      <>
        {hint}
        <Typography variant="body2" color="text.secondary">
          {t('settings.noViewers')}
        </Typography>
      </>
    )
  }
  return (
    <>
      {hint}
      <Table size="small">
        <TableHead>
          <TableRow>
            <TableCell align="right" sx={{ width: 48 }}>
              {t('grid.number')}
            </TableCell>
            <TableCell>{t('settings.viewer')}</TableCell>
            <TableCell>{t('settings.browser')}</TableCell>
            <TableCell>{t('settings.address')}</TableCell>
            <TableCell>{t('settings.watching')}</TableCell>
            <TableCell>{t('settings.since')}</TableCell>
            <TableCell>{t('settings.lastActive')}</TableCell>
            <TableCell align="right">{t('settings.updates')}</TableCell>
          </TableRow>
        </TableHead>
        <TableBody>
          {viewers.map((v, n) => {
            const { browser, system } = describeUserAgent(v.userAgent)
            return (
              <TableRow key={v.id}>
                <TableCell align="right" sx={{ color: 'text.secondary' }}>
                  {n + 1}
                </TableCell>
                <TableCell>
                  <Stack direction="row" spacing={1} alignItems="center">
                    <span>{v.login || v.name || t('settings.anonymous')}</span>
                    {v.kind === 'display' ? (
                      <Chip size="small" color="info" label={t('settings.wallDisplay')} />
                    ) : null}
                  </Stack>
                </TableCell>
                <TableCell title={v.userAgent}>
                  {[browser, system].filter(Boolean).join(' · ') || '—'}
                </TableCell>
                <TableCell>{v.ip || '—'}</TableCell>
                <TableCell>{(v.signals ?? []).join(', ')}</TableCell>
                <TableCell sx={{ whiteSpace: 'nowrap' }}>{time(v.connected)}</TableCell>
                <TableCell sx={{ whiteSpace: 'nowrap' }}>{time(v.lastSeen)}</TableCell>
                <TableCell align="right">{v.sent.toLocaleString()}</TableCell>
              </TableRow>
            )
          })}
        </TableBody>
      </Table>
    </>
  )
}
