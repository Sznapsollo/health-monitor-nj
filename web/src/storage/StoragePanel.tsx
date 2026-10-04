import ArchiveIcon from '@mui/icons-material/Archive'
import DeleteIcon from '@mui/icons-material/Delete'
import DownloadIcon from '@mui/icons-material/Download'
import Alert from '@mui/material/Alert'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Chip from '@mui/material/Chip'
import Dialog from '@mui/material/Dialog'
import DialogActions from '@mui/material/DialogActions'
import DialogContent from '@mui/material/DialogContent'
import DialogContentText from '@mui/material/DialogContentText'
import DialogTitle from '@mui/material/DialogTitle'
import IconButton from '@mui/material/IconButton'
import Stack from '@mui/material/Stack'
import Table from '@mui/material/Table'
import TableBody from '@mui/material/TableBody'
import TableCell from '@mui/material/TableCell'
import TableHead from '@mui/material/TableHead'
import TableRow from '@mui/material/TableRow'
import TextField from '@mui/material/TextField'
import Tooltip from '@mui/material/Tooltip'
import Typography from '@mui/material/Typography'
import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { BackupsSection } from '../app/BackupsSection'
import { Busy } from '../app/Busy'
import { gigabytes } from '../api/server'

import {
  archiveAlertDay,
  archiveDay,
  archiveDownloadUrl,
  deleteAlertDay,
  deleteArchive,
  deleteDay,
  fetchStorage,
  historyUrl,
  reclaimSpace,
  type ArchiveFile,
  type Retention,
  type Storage,
} from '../api/search'

function fileSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 ** 2) return `${Math.round(bytes / 1024)} KB`
  if (bytes < 1024 ** 3) return `${(bytes / 1024 ** 2).toFixed(1)} MB`
  return `${(bytes / 1024 ** 3).toFixed(2)} GB`
}

const RECLAIM_POLL_MS = 3000

function whenOf(iso: string): string {
  return new Date(iso).toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' })
}

/** What the database and the archive hold, and the actions that free space. */
export function StoragePanel() {
  const { t } = useTranslation()
  const [storage, setStorage] = useState<Storage | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [working, setWorking] = useState<string | null>(null)
  const [dayToDelete, setDayToDelete] = useState<string | null>(null)
  const [deletingAlerts, setDeletingAlerts] = useState(false)
  const [typedDay, setTypedDay] = useState('')
  const [fileToDelete, setFileToDelete] = useState<ArchiveFile | null>(null)

  const load = useCallback((signal?: AbortSignal) => {
    return fetchStorage(signal)
      .then(setStorage)
      .catch((err: unknown) => {
        if (!signal?.aborted) setError(err instanceof Error ? err.message : 'unknown error')
      })
  }, [])

  useEffect(() => {
    const controller = new AbortController()
    void load(controller.signal)
    return () => controller.abort()
  }, [load])

  const reclaiming = storage?.reclaiming ?? false
  useEffect(() => {
    if (!reclaiming) return
    const timer = setInterval(() => void load(), RECLAIM_POLL_MS)
    return () => clearInterval(timer)
  }, [reclaiming, load])

  async function run(key: string, action: () => Promise<void>) {
    setWorking(key)
    setError(null)
    try {
      await action()
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'unknown error')
    } finally {
      setWorking(null)
      await load()
    }
  }

  function confirmDeleteDay() {
    const day = dayToDelete
    setDayToDelete(null)
    setTypedDay('')
    if (day) void run(day, () => (deletingAlerts ? deleteAlertDay(day) : deleteDay(day)))
  }

  function confirmDeleteFile() {
    const file = fileToDelete
    setFileToDelete(null)
    if (file) void run(file.file, () => deleteArchive(file.file))
  }

  const days = storage?.days ?? []
  const archives = storage?.archives ?? []

  return (
    <Stack spacing={3}>
      {error ? <Alert severity="error">{error}</Alert> : null}

      {storage?.retention ? <RetentionHint retention={storage.retention} /> : null}

      {(storage?.disks ?? []).map((d) => (
        <Alert key={d.folders.join()} severity={d.low ? 'error' : 'success'} variant="outlined">
          {t(d.low ? 'storage.diskLow' : 'storage.disk', {
            free: gigabytes(d.freeBytes),
            total: gigabytes(d.totalBytes),
            folders: d.folders.map((f) => t(`storage.folder.${f}`)).join(', '),
          })}
        </Alert>
      ))}

      {storage ? (
        <Stack direction="row" spacing={2} alignItems="center" flexWrap="wrap" useFlexGap>
          <Typography variant="body2">
            {t('history.dbSize', { size: fileSize(storage.dbBytes) })}
            {storage.freeBytes > 0
              ? ` · ${t('history.freeSize', { size: fileSize(storage.freeBytes) })}`
              : ''}
          </Typography>
          {storage.archiving ? (
            <Typography variant="body2">
              {t('history.archiveSize', { size: fileSize(storage.archiveBytes) })}
            </Typography>
          ) : null}
          {reclaiming ? (
            <Chip size="small" label={t('history.reclaiming')} />
          ) : storage.freeBytes > 0 ? (
            <Button size="small" onClick={() => void run('reclaim', reclaimSpace)}>
              {t('history.reclaim')}
            </Button>
          ) : null}
        </Stack>
      ) : null}

      <Box>
        <Typography variant="subtitle1" gutterBottom>
          {t('history.days')}
        </Typography>
        <Typography variant="body2" color="text.secondary" gutterBottom>
          {t('storage.leavesHelp')}
        </Typography>
        {!storage ? (
          <Busy />
        ) : days.length === 0 ? (
          <Typography variant="body2" color="text.secondary">
            {t('history.none')}
          </Typography>
        ) : (
          <Table size="small">
            <TableHead>
              <TableRow>
                <TableCell>{t('history.day')}</TableCell>
                <TableCell align="right">{t('history.rows')}</TableCell>
                <TableCell align="right">{t('history.size')}</TableCell>
                <TableCell>{t('storage.leaves')}</TableCell>
                <TableCell align="right">{t('history.download')}</TableCell>
                <TableCell align="right">{t('history.actions')}</TableCell>
              </TableRow>
            </TableHead>
            <TableBody>
              {days.map((d) => (
                <TableRow key={d.day}>
                  <TableCell>{d.day}</TableCell>
                  <TableCell align="right">
                    {d.measuring ? t('storage.measuring') : d.rows.toLocaleString()}
                  </TableCell>
                  <TableCell align="right">
                    {d.measuring
                      ? null
                      : d.estimated
                        ? t('storage.approx', { size: fileSize(d.bytes) })
                        : fileSize(d.bytes)}
                  </TableCell>
                  <TableCell sx={{ whiteSpace: 'nowrap' }}>
                    {(d.leaves ?? []).map((l) => (
                      <div key={l.kind}>
                        {t(l.archive ? 'storage.toFile' : 'storage.dropped', {
                          kind: l.name ?? l.kind,
                          when: whenOf(l.on),
                        })}
                      </div>
                    ))}
                  </TableCell>
                  <TableCell align="right">
                    <Button
                      size="small"
                      startIcon={<DownloadIcon />}
                      href={historyUrl(d.day, 'csv')}
                    >
                      {t('history.csv')}
                    </Button>
                    <Button size="small" href={historyUrl(d.day, 'json')}>
                      {t('history.json')}
                    </Button>
                  </TableCell>
                  <TableCell align="right">
                    {d.today ? (
                      <Chip size="small" label={t('history.today')} />
                    ) : working === d.day ? (
                      <Busy label={t('history.working')} />
                    ) : (
                      <>
                        {storage.archiving ? (
                          <Tooltip title={t('history.archiveHelp')}>
                            <span>
                              <Button
                                size="small"
                                startIcon={<ArchiveIcon />}
                                disabled={working !== null}
                                onClick={() => void run(d.day, () => archiveDay(d.day))}
                              >
                                {t('history.archive')}
                              </Button>
                            </span>
                          </Tooltip>
                        ) : null}
                        <Tooltip title={t('history.deleteDay')}>
                          <span>
                            <IconButton
                              size="small"
                              aria-label={t('history.deleteDay')}
                              disabled={working !== null}
                              onClick={() => {
                                setDeletingAlerts(false)
                                setDayToDelete(d.day)
                              }}
                            >
                              <DeleteIcon fontSize="small" />
                            </IconButton>
                          </span>
                        </Tooltip>
                      </>
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </Box>

      {storage && storage.alertDays.length > 0 ? (
        <Box>
          <Typography variant="subtitle1" gutterBottom>
            {t('storage.alertDays')}
          </Typography>
          <Table size="small">
            <TableHead>
              <TableRow>
                <TableCell>{t('history.day')}</TableCell>
                <TableCell align="right">{t('storage.alertCount')}</TableCell>
                <TableCell>{t('storage.leaves')}</TableCell>
                <TableCell align="right">{t('history.actions')}</TableCell>
              </TableRow>
            </TableHead>
            <TableBody>
              {storage.alertDays.map((a) => (
                <TableRow key={a.day}>
                  <TableCell>{a.day}</TableCell>
                  <TableCell align="right">{a.alerts.toLocaleString()}</TableCell>
                  <TableCell sx={{ whiteSpace: 'nowrap' }}>
                    {a.leaves
                      ? t(a.archive ? 'storage.alertsToFile' : 'storage.alertsDropped', {
                          when: whenOf(a.leaves),
                        })
                      : null}
                  </TableCell>
                  <TableCell align="right">
                    {a.today ? (
                      <Chip size="small" label={t('history.today')} />
                    ) : working === `alerts-${a.day}` ? (
                      <Busy label={t('history.working')} />
                    ) : (
                      <>
                        {storage.archiving ? (
                          <Tooltip title={t('storage.archiveAlertsHelp')}>
                            <span>
                              <Button
                                size="small"
                                startIcon={<ArchiveIcon />}
                                disabled={working !== null}
                                onClick={() =>
                                  void run(`alerts-${a.day}`, () => archiveAlertDay(a.day))
                                }
                              >
                                {t('history.archive')}
                              </Button>
                            </span>
                          </Tooltip>
                        ) : null}
                        <Tooltip title={t('storage.deleteAlertDay')}>
                          <span>
                            <IconButton
                              size="small"
                              aria-label={t('storage.deleteAlertDay')}
                              disabled={working !== null}
                              onClick={() => {
                                setDeletingAlerts(true)
                                setDayToDelete(a.day)
                              }}
                            >
                              <DeleteIcon fontSize="small" />
                            </IconButton>
                          </span>
                        </Tooltip>
                      </>
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </Box>
      ) : null}

      {storage?.archiving ? (
        <Box>
          <Typography variant="subtitle1" gutterBottom>
            {t('history.archives')}
          </Typography>
          <Typography variant="body2" color="text.secondary" sx={{ mb: 1 }}>
            {t('history.archivesHelp')}
          </Typography>
          {archives.length === 0 ? (
            <Typography variant="body2" color="text.secondary">
              {t('history.noArchives')}
            </Typography>
          ) : (
            <Table size="small">
              <TableHead>
                <TableRow>
                  <TableCell>{t('history.day')}</TableCell>
                  <TableCell>{t('history.kind')}</TableCell>
                  <TableCell align="right">{t('history.size')}</TableCell>
                  <TableCell>{t('storage.deleteOn')}</TableCell>
                  <TableCell align="right">{t('history.download')}</TableCell>
                </TableRow>
              </TableHead>
              <TableBody>
                {archives.map((f) => (
                  <TableRow key={f.file}>
                    <TableCell>{f.day}</TableCell>
                    <TableCell>{f.alerts ? t('storage.alerts') : (f.name ?? f.kind)}</TableCell>
                    <TableCell align="right">{fileSize(f.bytes)}</TableCell>
                    <TableCell sx={{ whiteSpace: 'nowrap' }}>
                      {f.deleteOn ? whenOf(f.deleteOn) : t('storage.keptForGood')}
                    </TableCell>
                    <TableCell align="right">
                      <Button
                        size="small"
                        startIcon={<DownloadIcon />}
                        href={archiveDownloadUrl(f.file)}
                      >
                        {t('history.gzip')}
                      </Button>
                      <Tooltip title={t('history.deleteArchive')}>
                        <span>
                          <IconButton
                            size="small"
                            aria-label={t('history.deleteArchive')}
                            disabled={working !== null}
                            onClick={() => setFileToDelete(f)}
                          >
                            <DeleteIcon fontSize="small" />
                          </IconButton>
                        </span>
                      </Tooltip>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </Box>
      ) : null}

      <Box>
        <Typography variant="subtitle1" gutterBottom>
          {t('storage.backups')}
        </Typography>
        <BackupsSection />
      </Box>

      <Dialog
        open={dayToDelete !== null}
        onClose={() => {
          setDayToDelete(null)
          setTypedDay('')
        }}
      >
        <DialogTitle>{t('history.deleteDay')}</DialogTitle>
        <DialogContent>
          <DialogContentText sx={{ mb: 2 }}>
            {t(deletingAlerts ? 'storage.deleteAlertDayConfirm' : 'history.deleteDayConfirm', {
              day: dayToDelete ?? '',
            })}
          </DialogContentText>
          <TextField
            autoFocus
            fullWidth
            size="small"
            label={t('history.typeDay')}
            value={typedDay}
            onChange={(e) => setTypedDay(e.target.value)}
          />
        </DialogContent>
        <DialogActions>
          <Button
            onClick={() => {
              setDayToDelete(null)
              setTypedDay('')
            }}
          >
            {t('history.cancel')}
          </Button>
          <Button color="error" disabled={typedDay !== dayToDelete} onClick={confirmDeleteDay}>
            {t('history.deleteDay')}
          </Button>
        </DialogActions>
      </Dialog>

      <Dialog open={fileToDelete !== null} onClose={() => setFileToDelete(null)}>
        <DialogTitle>{t('history.deleteArchive')}</DialogTitle>
        <DialogContent>
          <DialogContentText>
            {t('history.deleteArchiveConfirm', { file: fileToDelete?.file ?? '' })}
          </DialogContentText>
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setFileToDelete(null)}>{t('history.cancel')}</Button>
          <Button color="error" onClick={confirmDeleteFile}>
            {t('history.deleteArchive')}
          </Button>
        </DialogActions>
      </Dialog>
    </Stack>
  )
}

function RetentionHint({ retention }: { retention: NonNullable<Storage['retention']> }) {
  const { t } = useTranslation()
  const line = (kind: string, r: Retention) => {
    const rule =
      r.archiveDays > 0
        ? t('storage.keptThenArchived', { db: r.dbDays, archive: r.archiveDays })
        : t('storage.keptThenDropped', { db: r.dbDays })
    return t('storage.kindRule', { kind, rule })
  }
  return (
    <Alert severity="info">
      {(retention.kinds ?? []).map((r) => (
        <Typography variant="body2" key={r.kind}>
          {line(r.name ?? r.kind ?? '', r)}
        </Typography>
      ))}
      <Typography variant="body2">{line(t('storage.otherKinds'), retention.default)}</Typography>
      {retention.alerts ? (
        <Typography variant="body2">{line(t('storage.alerts'), retention.alerts)}</Typography>
      ) : null}
    </Alert>
  )
}
