import Chip from '@mui/material/Chip'
import MenuItem from '@mui/material/MenuItem'
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

import { Busy } from './Busy'
import { everyVisible } from './everyVisible'

import { fetchVisitDays, fetchVisits, type Visit } from '../api/session'
import { describeUserAgent } from './userAgent'

function duration(ms: number): string {
  const minutes = Math.max(0, Math.round(ms / 60_000))
  const h = Math.floor(minutes / 60)
  return h > 0 ? `${h} h ${minutes % 60} min` : `${minutes} min`
}

function dayLabel(day: string): string {
  return `${day.slice(0, 4)}-${day.slice(4, 6)}-${day.slice(6)}`
}

const REFRESH_MS = 15_000

/** Who had the monitor open on a day: one row per browser tab or wall screen. */
export function VisitsTable() {
  const { t } = useTranslation()
  const [days, setDays] = useState<string[]>([])
  const [day, setDay] = useState('')
  const [visits, setVisits] = useState<Visit[] | null>(null)

  useEffect(() => {
    const controller = new AbortController()
    fetchVisitDays(controller.signal)
      .then((list) => {
        setDays(list)
        setDay((current) => current || list[0] || '')
      })
      .catch(() => setDays([]))
    return () => controller.abort()
  }, [])

  useEffect(() => {
    if (!day) return
    const controller = new AbortController()
    const load = () => {
      fetchVisits(day, controller.signal)
        .then(setVisits)
        .catch(() => {
          if (!controller.signal.aborted) setVisits((have) => have ?? [])
        })
    }
    load()
    const stop = everyVisible(load, REFRESH_MS)
    return () => {
      controller.abort()
      stop()
    }
  }, [day])

  const time = (iso: string) => new Date(iso).toLocaleString()
  return (
    <Stack spacing={1}>
      <Typography variant="body2" color="text.secondary">
        {t('settings.visitsHelp')}
      </Typography>
      {days.length > 0 ? (
        <TextField
          select
          size="small"
          label={t('settings.visitsDay')}
          value={day}
          onChange={(e) => setDay(e.target.value)}
          sx={{ maxWidth: 200 }}
        >
          {days.map((d) => (
            <MenuItem key={d} value={d}>
              {dayLabel(d)}
            </MenuItem>
          ))}
        </TextField>
      ) : null}
      {visits === null ? (
        <Busy />
      ) : visits.length === 0 ? (
        <Typography variant="body2" color="text.secondary">
          {t('settings.noVisits')}
        </Typography>
      ) : (
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
              <TableCell>{t('settings.visitFrom')}</TableCell>
              <TableCell>{t('settings.visitTo')}</TableCell>
              <TableCell align="right">{t('settings.visitLength')}</TableCell>
              <TableCell align="right">{t('settings.reconnects')}</TableCell>
              <TableCell align="right">{t('settings.updates')}</TableCell>
            </TableRow>
          </TableHead>
          <TableBody>
            {visits.map((v, n) => {
              const { browser, system } = describeUserAgent(v.userAgent)
              const end = v.open ? Date.now() : new Date(v.ended ?? v.lastSeen).getTime()
              return (
                <TableRow key={v.id}>
                  <TableCell align="right" sx={{ color: 'text.secondary' }}>
                    {n + 1}
                  </TableCell>
                  <TableCell>
                    <Stack direction="row" spacing={1} alignItems="center">
                      <span>{v.login || t('settings.anonymous')}</span>
                      {v.kind === 'display' ? (
                        <Chip size="small" color="info" label={t('settings.wallDisplay')} />
                      ) : null}
                    </Stack>
                  </TableCell>
                  <TableCell title={v.userAgent}>
                    {[browser, system].filter(Boolean).join(' · ') || '—'}
                  </TableCell>
                  <TableCell>{v.ip || '—'}</TableCell>
                  <TableCell>{v.signals.join(', ') || '—'}</TableCell>
                  <TableCell sx={{ whiteSpace: 'nowrap' }}>{time(v.started)}</TableCell>
                  <TableCell sx={{ whiteSpace: 'nowrap' }}>
                    {v.open ? (
                      <Chip size="small" color="success" label={t('settings.stillOpen')} />
                    ) : (
                      time(v.ended ?? v.lastSeen)
                    )}
                  </TableCell>
                  <TableCell align="right" sx={{ whiteSpace: 'nowrap' }}>
                    {duration(end - new Date(v.started).getTime())}
                  </TableCell>
                  <TableCell align="right">{v.reconnects}</TableCell>
                  <TableCell align="right">{v.sent.toLocaleString()}</TableCell>
                </TableRow>
              )
            })}
          </TableBody>
        </Table>
      )}
    </Stack>
  )
}
