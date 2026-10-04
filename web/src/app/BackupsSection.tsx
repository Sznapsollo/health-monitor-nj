import BackupIcon from '@mui/icons-material/Backup'
import DeleteIcon from '@mui/icons-material/Delete'
import DownloadIcon from '@mui/icons-material/Download'
import Alert from '@mui/material/Alert'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
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
import Tooltip from '@mui/material/Tooltip'
import Typography from '@mui/material/Typography'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Busy } from './Busy'

import {
  backupDownloadUrl,
  deleteBackup,
  fetchBackups,
  requestBackup,
  type BackupFile,
  type BackupList,
} from '../api/search'

function megabytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} KB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
}

/** Database backups: make one, download one, delete one. */
export function BackupsSection() {
  const { t } = useTranslation()
  const [message, setMessage] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  // Backups stay on the server; the list is how you fetch a copy later rather
  // than only in the moment one is made.
  const [backups, setBackups] = useState<BackupFile[]>([])
  const [limit, setLimit] = useState(3)
  const [loading, setLoading] = useState(true)
  const [writing, setWriting] = useState(false)
  // Deleting a backup cannot be undone, so it is asked about by name.
  const [toDelete, setToDelete] = useState<BackupFile | null>(null)

  useEffect(() => {
    const controller = new AbortController()
    fetchBackups(controller.signal)
      .then(show)
      .catch(() => setBackups([]))
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false)
      })
    return () => controller.abort()
  }, [])

  function show(list: BackupList) {
    setBackups(list.backups)
    setLimit(list.limit)
  }
  const full = backups.length >= limit

  const backup = async () => {
    setError(null)
    setMessage(null)
    setWriting(true)
    try {
      const res = await requestBackup()
      setMessage(t('history.backupWritten', { file: res.file }))
    } catch (err) {
      setError(err instanceof Error ? err.message : 'unknown error')
      return
    } finally {
      setWriting(false)
    }
    // The file is on disk by now. A listing that fails afterwards is worth
    // nothing more than an empty list: it must not be reported as though the
    // backup itself had gone wrong.
    try {
      show(await fetchBackups())
    } catch {
      // The list refreshes on the next visit.
    }
  }

  const removeBackup = async (file: BackupFile) => {
    setError(null)
    setToDelete(null)
    try {
      await deleteBackup(file.file)
      setMessage(t('history.backupDeleted', { file: file.file }))
      show(await fetchBackups())
    } catch (err) {
      setError(err instanceof Error ? err.message : 'unknown error')
    }
  }

  return (
    <Stack spacing={1}>
      {error ? <Alert severity="error">{error}</Alert> : null}
      {message ? <Alert severity="success">{message}</Alert> : null}
      <Box>
        <Stack direction="row" spacing={1} alignItems="center">
          <Tooltip title={full ? t('history.backupLimit', { limit }) : ''}>
            {/* A disabled button fires no events, so the tooltip needs a wrapper. */}
            <span>
              <Button
                size="small"
                startIcon={<BackupIcon />}
                disabled={writing || full}
                onClick={() => void backup()}
              >
                {t('history.backup')}
              </Button>
            </span>
          </Tooltip>
          {writing ? (
            <Busy label={t('history.backingUp')} />
          ) : (
            <Typography variant="body2" color={full ? 'warning.main' : 'text.secondary'}>
              {full ? t('history.backupLimit', { limit }) : t('history.backupHelp')}
            </Typography>
          )}
        </Stack>
        {loading ? <Busy /> : null}

        {backups.length > 0 ? (
          <Table size="small" sx={{ mt: 1 }}>
            <TableHead>
              <TableRow>
                <TableCell>{t('history.backupFile')}</TableCell>
                <TableCell align="right">{t('history.size')}</TableCell>
                <TableCell align="right">{t('history.download')}</TableCell>
              </TableRow>
            </TableHead>
            <TableBody>
              {backups.map((b) => (
                <TableRow key={b.file}>
                  <TableCell>{b.file}</TableCell>
                  <TableCell align="right">{megabytes(b.bytes)}</TableCell>
                  <TableCell align="right">
                    <Button
                      size="small"
                      startIcon={<DownloadIcon />}
                      href={backupDownloadUrl(b.file)}
                    >
                      {t('history.download')}
                    </Button>
                    <Tooltip title={t('history.deleteBackup')}>
                      <IconButton
                        size="small"
                        aria-label={t('history.deleteBackup')}
                        onClick={() => setToDelete(b)}
                      >
                        <DeleteIcon fontSize="small" />
                      </IconButton>
                    </Tooltip>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        ) : null}
      </Box>

      <Dialog open={toDelete !== null} onClose={() => setToDelete(null)}>
        <DialogTitle>{t('history.deleteBackup')}</DialogTitle>
        <DialogContent>
          <DialogContentText>
            {t('history.deleteBackupConfirm', { file: toDelete?.file ?? '' })}
          </DialogContentText>
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setToDelete(null)}>{t('history.cancel')}</Button>
          <Button color="error" onClick={() => toDelete && void removeBackup(toDelete)}>
            {t('history.deleteBackup')}
          </Button>
        </DialogActions>
      </Dialog>
    </Stack>
  )
}
