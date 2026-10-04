import AddIcon from '@mui/icons-material/Add'
import DeleteIcon from '@mui/icons-material/Delete'
import EditIcon from '@mui/icons-material/Edit'
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

import { deleteSilence, type Silence } from '../api/alerts'
import { useAlertStore } from '../store/useAlertStore'
import { SilenceDialog } from './SilenceDialog'
import { describeTarget } from './silenceTargets'

/** The silence rules: what is hidden from the alerts, why and until when. */
export function SilenceRules({ platform }: { platform: string }) {
  const { t, i18n } = useTranslation()
  const { silences, review, loadSilences, categories } = useAlertStore()
  const [creating, setCreating] = useState(false)
  const [editing, setEditing] = useState<Silence | null>(null)

  const time = useMemo(
    () => new Intl.DateTimeFormat(i18n.language, { dateStyle: 'short', timeStyle: 'short' }),
    [i18n.language],
  )

  const remove = async (id: string) => {
    await deleteSilence(id)
    await loadSilences(platform)
  }

  return (
    <Stack spacing={2}>
      <Box>
        <Button startIcon={<AddIcon />} onClick={() => setCreating(true)}>
          {t('maintenance.new')}
        </Button>
      </Box>
      <SilenceDialog
        open={creating}
        platform={platform}
        message=""
        category=""
        categories={categories}
        onClose={() => setCreating(false)}
        onCreated={() => void loadSilences(platform)}
      />
      <SilenceDialog
        key={editing?.id ?? ''}
        open={editing !== null}
        platform={platform}
        message=""
        category=""
        categories={categories}
        editing={editing ?? undefined}
        onClose={() => setEditing(null)}
        onCreated={() => void loadSilences(platform)}
      />
      {review.length > 0 ? (
        <Alert severity="info">{t('maintenance.review', { count: review.length })}</Alert>
      ) : null}

      {silences.length === 0 ? (
        <Typography variant="body2" color="text.secondary">
          {t('maintenance.none')}
        </Typography>
      ) : (
        <Box sx={{ overflowX: 'auto' }}>
          <Table size="small">
            <TableHead>
              <TableRow>
                <TableCell>{t('maintenance.target')}</TableCell>
                <TableCell>{t('maintenance.kind')}</TableCell>
                <TableCell>{t('maintenance.reason')}</TableCell>
                <TableCell>{t('maintenance.by')}</TableCell>
                <TableCell>{t('maintenance.until')}</TableCell>
                <TableCell align="right">{t('maintenance.suppressed')}</TableCell>
                <TableCell align="right" />
              </TableRow>
            </TableHead>
            <TableBody>
              {silences.map((s) => (
                <TableRow key={s.id}>
                  <TableCell>
                    <Typography variant="body2">{describeTarget(s.target, t)}</Typography>
                    <Typography
                      variant="caption"
                      color="text.secondary"
                      sx={{ fontFamily: 'monospace' }}
                    >
                      {s.target}
                    </Typography>
                  </TableCell>
                  <TableCell>
                    <Chip size="small" label={t(`silence.${s.kind}`)} />
                  </TableCell>
                  <TableCell>{s.reason}</TableCell>
                  <TableCell>{s.by}</TableCell>
                  <TableCell sx={{ whiteSpace: 'nowrap' }}>
                    {s.until ? time.format(new Date(s.until)) : t('maintenance.indefinite')}
                  </TableCell>
                  <TableCell align="right">{s.suppressed}</TableCell>
                  <TableCell align="right" sx={{ whiteSpace: 'nowrap' }}>
                    <Tooltip title={t('maintenance.edit')}>
                      <IconButton
                        size="small"
                        aria-label={t('maintenance.edit')}
                        onClick={() => setEditing(s)}
                      >
                        <EditIcon fontSize="small" />
                      </IconButton>
                    </Tooltip>
                    <Tooltip title={t('maintenance.unsilence')}>
                      <IconButton
                        size="small"
                        aria-label={t('maintenance.unsilence')}
                        onClick={() => void remove(s.id)}
                      >
                        <DeleteIcon fontSize="small" />
                      </IconButton>
                    </Tooltip>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </Box>
      )}
    </Stack>
  )
}
