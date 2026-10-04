import Alert from '@mui/material/Alert'
import Button from '@mui/material/Button'
import Chip from '@mui/material/Chip'
import MenuItem from '@mui/material/MenuItem'
import Paper from '@mui/material/Paper'
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

import {
  fetchInfoEntries,
  fetchInfoReport,
  forgetInfo,
  type InfoEntry,
  type InfoReport,
} from '../api/info'
import { Busy } from '../app/Busy'
import { everyVisible } from '../app/everyVisible'
import { JsonTree } from './JsonTree'

const REFRESH_MS = 15_000

interface Props {
  platform: string
}

const idOf = (e: { signal: string; key: string }) => `${e.signal}\u0000${e.key}`

/** Reports kept by info signals: the latest of each sender, and the few before it. */
export function InfoPanel({ platform }: Props) {
  const { t } = useTranslation()
  const [entries, setEntries] = useState<InfoEntry[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [picked, setPicked] = useState<string | null>(null)

  const [reload, setReload] = useState(0)

  useEffect(() => {
    if (!platform) return
    const controller = new AbortController()
    const load = () => {
      fetchInfoEntries(platform, controller.signal)
        .then((list) => {
          setEntries(list)
          setError(null)
        })
        .catch((err: unknown) => {
          if (controller.signal.aborted) return
          setError(err instanceof Error ? err.message : 'unknown error')
          setEntries((have) => have ?? [])
        })
    }
    load()
    const stop = everyVisible(load, REFRESH_MS)
    return () => {
      controller.abort()
      stop()
    }
  }, [platform, reload])

  const remove = async (entry: InfoEntry) => {
    if (
      !globalThis.confirm(t('info.confirmRemove', { signal: entry.displayName, sender: entry.key }))
    )
      return
    try {
      await forgetInfo(platform, entry)
      setPicked(null)
      setReload((n) => n + 1)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'unknown error')
    }
  }

  if (entries === null) return <Busy />

  const selected = entries.find((e) => idOf(e) === picked) ?? entries[0] ?? null

  return (
    <Stack spacing={2}>
      {error ? <Alert severity="error">{error}</Alert> : null}
      {entries.length === 0 ? (
        <Typography variant="body2" color="text.secondary">
          {t('info.none')}
        </Typography>
      ) : (
        <Table size="small">
          <TableHead>
            <TableRow>
              <TableCell align="right" sx={{ width: 48 }}>
                {t('grid.number')}
              </TableCell>
              <TableCell>{t('info.signal')}</TableCell>
              <TableCell>{t('info.sender')}</TableCell>
              <TableCell>{t('info.state')}</TableCell>
              <TableCell>{t('info.received')}</TableCell>
              <TableCell align="right">{t('info.versions')}</TableCell>
            </TableRow>
          </TableHead>
          <TableBody>
            {entries.map((e, i) => (
              <TableRow
                key={idOf(e)}
                hover
                selected={selected !== null && idOf(e) === idOf(selected)}
                onClick={() => setPicked(idOf(e))}
                sx={{ cursor: 'pointer' }}
              >
                <TableCell align="right" sx={{ color: 'text.secondary' }}>
                  {i + 1}
                </TableCell>
                <TableCell title={e.packetType}>{e.displayName}</TableCell>
                <TableCell>{e.key}</TableCell>
                <TableCell>
                  {e.noStatus ? null : (
                    <Chip
                      size="small"
                      color={e.offline ? 'error' : 'success'}
                      label={t(e.offline ? 'info.down' : 'info.up')}
                    />
                  )}
                </TableCell>
                <TableCell>{new Date(e.received).toLocaleString()}</TableCell>
                <TableCell align="right">{e.versions}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
      {selected ? (
        <Report
          key={idOf(selected)}
          platform={platform}
          entry={selected}
          onRemove={() => void remove(selected)}
        />
      ) : null}
    </Stack>
  )
}

function Report({
  platform,
  entry,
  onRemove,
}: {
  platform: string
  entry: InfoEntry
  onRemove: () => void
}) {
  const { t } = useTranslation()
  const [version, setVersion] = useState<number | null>(null)
  const [report, setReport] = useState<InfoReport | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [search, setSearch] = useState('')

  // The latest follows new arrivals; an older version stays put.
  const refresh = version === null ? entry.received : ''
  const { signal, key } = entry
  useEffect(() => {
    const controller = new AbortController()
    fetchInfoReport(platform, { signal, key }, version, controller.signal)
      .then((r) => {
        setReport(r)
        setError(null)
      })
      .catch((err: unknown) => {
        if (!controller.signal.aborted)
          setError(err instanceof Error ? err.message : 'unknown error')
      })
    return () => controller.abort()
  }, [platform, signal, key, version, refresh])

  return (
    <Paper variant="outlined" sx={{ p: 2 }}>
      <Stack spacing={2}>
        <Stack direction="row" alignItems="center" justifyContent="space-between" spacing={1}>
          <Typography variant="subtitle1">
            {t('info.reportOf', { signal: entry.displayName, sender: entry.key })}
          </Typography>
          <Button size="small" color="error" onClick={onRemove}>
            {t('info.remove')}
          </Button>
        </Stack>
        {!entry.defined ? <Alert severity="warning">{t('info.undefined')}</Alert> : null}
        {entry.merge ? (
          <Typography variant="body2" color="text.secondary">
            {t('info.merged')}
          </Typography>
        ) : null}
        {error ? <Alert severity="error">{error}</Alert> : null}
        <Stack direction={{ xs: 'column', md: 'row' }} spacing={2}>
          <TextField
            select
            size="small"
            label={t('info.version')}
            value={version === null ? 'latest' : String(version)}
            onChange={(e) =>
              setVersion(e.target.value === 'latest' ? null : Number(e.target.value))
            }
            sx={{ minWidth: 260 }}
          >
            <MenuItem value="latest">{t('info.latest')}</MenuItem>
            {(report?.versions ?? []).map((v) => (
              <MenuItem key={v.id} value={String(v.id)}>
                {t('info.versionAt', {
                  when: new Date(v.received).toLocaleString(),
                  size: Math.max(1, Math.round(v.size / 1024)),
                })}
              </MenuItem>
            ))}
          </TextField>
          <TextField
            size="small"
            label={t('info.search')}
            helperText={t('info.searchHelp')}
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            sx={{ minWidth: 260 }}
          />
        </Stack>
        {report ? (
          <>
            <Typography variant="body2" color="text.secondary">
              {t('info.receivedAt', { when: new Date(report.received).toLocaleString() })}
            </Typography>
            <JsonTree value={report.content} search={search} />
          </>
        ) : error ? null : (
          <Busy />
        )}
      </Stack>
    </Paper>
  )
}
