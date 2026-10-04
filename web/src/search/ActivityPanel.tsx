import Alert from '@mui/material/Alert'
import FormControl from '@mui/material/FormControl'
import InputLabel from '@mui/material/InputLabel'
import MenuItem from '@mui/material/MenuItem'
import Select from '@mui/material/Select'
import Stack from '@mui/material/Stack'
import Table from '@mui/material/Table'
import TableBody from '@mui/material/TableBody'
import TableCell from '@mui/material/TableCell'
import TableHead from '@mui/material/TableHead'
import TablePagination from '@mui/material/TablePagination'
import TableRow from '@mui/material/TableRow'
import Typography from '@mui/material/Typography'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Busy } from '../app/Busy'

import { fetchActivity, fetchActivityDays, type Activity } from '../api/search'

const PAGE_SIZES = [25, 50, 100, 250]

/** Who used the monitored platform on a day, worked out from that day's logs. */
export function ActivityPanel() {
  const { t } = useTranslation()
  const [days, setDays] = useState<string[] | null>(null)
  const [selected, setSelected] = useState('')
  const [activity, setActivity] = useState<Activity[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [paging, setPaging] = useState({ day: '', page: 0, size: 50 })

  useEffect(() => {
    const controller = new AbortController()
    fetchActivityDays(controller.signal)
      .then((list) => {
        setDays(list)
        setSelected(list[0] ?? '')
      })
      .catch((err: unknown) => {
        if (!controller.signal.aborted) {
          setError(err instanceof Error ? err.message : 'unknown error')
          setDays([])
        }
      })
    return () => controller.abort()
  }, [])

  useEffect(() => {
    if (!selected) return
    const controller = new AbortController()
    setLoading(true)
    fetchActivity(selected, controller.signal)
      .then(setActivity)
      .catch(() => setActivity([]))
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false)
      })
    return () => controller.abort()
  }, [selected])

  if (days === null) return <Busy />

  const lastPage = Math.max(0, Math.ceil(activity.length / paging.size) - 1)
  const page = paging.day === selected ? Math.min(paging.page, lastPage) : 0
  const first = page * paging.size
  const rows = activity.slice(first, first + paging.size)

  return (
    <Stack spacing={2}>
      {error ? <Alert severity="error">{error}</Alert> : null}
      <Typography variant="body2" color="text.secondary">
        {t('history.activityHelp')}
      </Typography>
      {days.length === 0 ? (
        <Typography variant="body2" color="text.secondary">
          {t('history.none')}
        </Typography>
      ) : (
        <FormControl size="small" sx={{ minWidth: 200, alignSelf: 'flex-start' }}>
          <InputLabel id="activity-day">{t('history.day')}</InputLabel>
          <Select
            labelId="activity-day"
            label={t('history.day')}
            value={selected}
            onChange={(e) => setSelected(String(e.target.value))}
          >
            {days.map((day) => (
              <MenuItem key={day} value={day}>
                {day}
              </MenuItem>
            ))}
          </Select>
        </FormControl>
      )}
      {!selected ? null : loading ? (
        <Busy label={t('history.computing')} />
      ) : activity.length === 0 ? (
        <Typography variant="body2" color="text.secondary">
          {t('history.noActivity')}
        </Typography>
      ) : (
        <>
          <Table size="small">
            <TableHead>
              <TableRow>
                <TableCell align="right" sx={{ width: 48 }}>
                  {t('grid.number')}
                </TableCell>
                <TableCell>{t('history.user')}</TableCell>
                <TableCell>{t('history.account')}</TableCell>
                <TableCell align="right">{t('history.minutes')}</TableCell>
                <TableCell align="right">{t('history.events')}</TableCell>
              </TableRow>
            </TableHead>
            <TableBody>
              {rows.map((a, i) => (
                <TableRow key={`${a.account}-${a.user}`}>
                  <TableCell align="right" sx={{ color: 'text.secondary' }}>
                    {first + i + 1}
                  </TableCell>
                  <TableCell>{a.user}</TableCell>
                  <TableCell>{a.account}</TableCell>
                  <TableCell align="right">{a.minutes}</TableCell>
                  <TableCell align="right">{a.events}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
          <TablePagination
            component="div"
            count={activity.length}
            page={page}
            rowsPerPage={paging.size}
            rowsPerPageOptions={PAGE_SIZES}
            onPageChange={(_, next) => setPaging({ day: selected, page: next, size: paging.size })}
            onRowsPerPageChange={(e) =>
              setPaging({ day: selected, page: 0, size: Number(e.target.value) })
            }
            labelRowsPerPage={t('alerts.perPage')}
            labelDisplayedRows={({ from, to, count }) => t('alerts.pageOf', { from, to, count })}
          />
        </>
      )}
    </Stack>
  )
}
