import Alert from '@mui/material/Alert'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Checkbox from '@mui/material/Checkbox'
import Chip from '@mui/material/Chip'
import Dialog from '@mui/material/Dialog'
import DialogActions from '@mui/material/DialogActions'
import DialogContent from '@mui/material/DialogContent'
import DialogTitle from '@mui/material/DialogTitle'
import FormControlLabel from '@mui/material/FormControlLabel'
import Stack from '@mui/material/Stack'
import Table from '@mui/material/Table'
import TableBody from '@mui/material/TableBody'
import TableCell from '@mui/material/TableCell'
import TableHead from '@mui/material/TableHead'
import TableRow from '@mui/material/TableRow'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import { Fragment, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { fetchPackets, type ReceivedPacket } from '../api/packets'
import { JsonTree } from '../info/JsonTree'
import { everyVisible } from './everyVisible'

const KEEP = 500

interface Props {
  open: boolean
  onClose: () => void
  /** Opens filtered to one sender, as the packets name it. */
  sender?: string
}

/** The datagrams the monitor receives while this is open, newest first. */
export function PacketsDialog({ open, onClose, sender: initialSender }: Props) {
  const { t, i18n } = useTranslation()
  const [packets, setPackets] = useState<ReceivedPacket[]>([])
  const [error, setError] = useState<string | null>(null)
  const [filter, setFilter] = useState('')
  const [noSender, setNoSender] = useState(false)
  const [sender, setSender] = useState(initialSender ?? '')
  const [paused, setPaused] = useState(false)
  const [picked, setPicked] = useState<number | null>(null)
  const since = useRef(0)

  useEffect(() => {
    if (!open || paused) return
    let live = true
    const load = async () => {
      try {
        const page = await fetchPackets(since.current)
        if (!live) return
        since.current = page.seq
        setError(null)
        if (page.entries.length > 0) {
          setPackets((have) => [...page.entries.reverse(), ...have].slice(0, KEEP))
        }
      } catch (e) {
        if (live) setError(String(e))
      }
    }
    void load()
    const stop = everyVisible(() => void load(), 2_000)
    return () => {
      live = false
      stop()
    }
  }, [open, paused])

  const time = useMemo(
    () => new Intl.DateTimeFormat(i18n.language, { timeStyle: 'medium' }),
    [i18n.language],
  )

  const shown = useMemo(() => {
    const needle = filter.trim().toLowerCase()
    return packets.filter(
      (p) =>
        (!noSender || !p.sender) &&
        (sender === '' || p.sender === sender) &&
        (needle === '' ||
          [p.sender, p.type, p.from, p.platform, p.rejected, p.raw].some((v) =>
            v?.toLowerCase().includes(needle),
          )),
    )
  }, [packets, filter, noSender, sender])

  return (
    <Dialog open={open} onClose={onClose} fullWidth maxWidth="lg">
      <DialogTitle>{t('packets.title')}</DialogTitle>
      <DialogContent>
        <Stack spacing={2}>
          <Typography variant="body2" color="text.secondary">
            {t('packets.help', { keep: KEEP })}
          </Typography>
          <Stack direction={{ xs: 'column', sm: 'row' }} spacing={2} alignItems={{ sm: 'center' }}>
            {sender ? (
              <Chip label={t('packets.onlySender', { sender })} onDelete={() => setSender('')} />
            ) : null}
            <TextField
              size="small"
              label={t('packets.filter')}
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
            />
            <FormControlLabel
              control={
                <Checkbox checked={noSender} onChange={(e) => setNoSender(e.target.checked)} />
              }
              label={t('packets.noSender')}
            />
            <Button size="small" onClick={() => setPaused(!paused)}>
              {paused ? t('packets.resume') : t('packets.pause')}
            </Button>
            <Button size="small" onClick={() => setPackets([])}>
              {t('packets.clear')}
            </Button>
          </Stack>
          {error ? <Alert severity="error">{error}</Alert> : null}
          {shown.length === 0 ? (
            <Typography variant="body2" color="text.secondary">
              {packets.length === 0 ? t('packets.waiting') : t('packets.noMatch')}
            </Typography>
          ) : (
            <Box sx={{ overflowX: 'auto' }}>
              <Table size="small">
                <TableHead>
                  <TableRow>
                    <TableCell>{t('packets.at')}</TableCell>
                    <TableCell>{t('packets.from')}</TableCell>
                    <TableCell>{t('packets.sender')}</TableCell>
                    <TableCell>{t('packets.type')}</TableCell>
                    <TableCell>{t('packets.platform')}</TableCell>
                    <TableCell align="right">{t('packets.size')}</TableCell>
                  </TableRow>
                </TableHead>
                <TableBody>
                  {shown.map((p) => (
                    <Fragment key={p.seq}>
                      <TableRow
                        hover
                        selected={picked === p.seq}
                        onClick={() => setPicked(picked === p.seq ? null : p.seq)}
                        sx={{ cursor: 'pointer' }}
                      >
                        <TableCell>{time.format(new Date(p.at))}</TableCell>
                        <TableCell>{p.from ?? ''}</TableCell>
                        <TableCell sx={p.sender ? undefined : { color: 'warning.main' }}>
                          {p.sender || t('packets.unknown')}
                        </TableCell>
                        <TableCell sx={p.rejected ? { color: 'error.main' } : undefined}>
                          {p.rejected ? t('packets.rejected', { reason: p.rejected }) : p.type}
                        </TableCell>
                        <TableCell>{p.platform ?? ''}</TableCell>
                        <TableCell align="right">{p.size.toLocaleString()}</TableCell>
                      </TableRow>
                      {picked === p.seq ? (
                        <TableRow>
                          <TableCell colSpan={6}>
                            <Raw packet={p} />
                          </TableCell>
                        </TableRow>
                      ) : null}
                    </Fragment>
                  ))}
                </TableBody>
              </Table>
            </Box>
          )}
        </Stack>
      </DialogContent>
      <DialogActions>
        <Button onClick={onClose}>{t('packets.close')}</Button>
      </DialogActions>
    </Dialog>
  )
}

function Raw({ packet }: { packet: ReceivedPacket }) {
  const { t } = useTranslation()
  const parsed = useMemo(() => {
    if (packet.cut) return undefined
    try {
      return JSON.parse(packet.raw) as unknown
    } catch {
      return undefined
    }
  }, [packet])
  return (
    <Stack spacing={1}>
      {packet.cut ? (
        <Typography variant="body2" color="text.secondary">
          {t('packets.cut', { kept: packet.raw.length, size: packet.size })}
        </Typography>
      ) : null}
      {parsed === undefined ? (
        <Box
          component="pre"
          sx={{ m: 0, fontSize: 13, whiteSpace: 'pre-wrap', wordBreak: 'break-all' }}
        >
          {packet.raw}
        </Box>
      ) : (
        <JsonTree value={parsed} search="" />
      )}
    </Stack>
  )
}
