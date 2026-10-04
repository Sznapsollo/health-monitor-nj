import DownloadIcon from '@mui/icons-material/Download'
import SearchIcon from '@mui/icons-material/Search'
import ViewColumnIcon from '@mui/icons-material/ViewColumn'
import Alert from '@mui/material/Alert'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Collapse from '@mui/material/Collapse'
import FormControl from '@mui/material/FormControl'
import IconButton from '@mui/material/IconButton'
import InputLabel from '@mui/material/InputLabel'
import MenuItem from '@mui/material/MenuItem'
import Select from '@mui/material/Select'
import Stack from '@mui/material/Stack'
import Table from '@mui/material/Table'
import TableBody from '@mui/material/TableBody'
import TableCell from '@mui/material/TableCell'
import TableHead from '@mui/material/TableHead'
import TableRow from '@mui/material/TableRow'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Busy } from '../app/Busy'
import { PAIRED_ROWS } from '../theme/theme'

import { search, searchCsvUrl, type LogRow } from '../api/search'
import { ColumnsDialog } from './ColumnsDialog'
import {
  fieldOf,
  fieldsFound,
  fieldValue,
  loadColumns,
  saveColumns,
  type SearchColumn,
} from './columns'

const DAY_CHOICES = [1, 2, 7, 14]
const LIMITS = [100, 500, 2000]
const TYPING_PAUSE_MS = 300

/**
 * The old "Wyszukiwarka", with the one thing it could never do: previous days.
 * The rows come from SQLite, so the window is a query rather than whatever
 * happens to still be in memory.
 */
export function SearchPanel({ platform }: { platform: string }) {
  const { t, i18n } = useTranslation()
  const [kind, setKind] = useState('')
  const [text, setText] = useState('')
  const [account, setAccount] = useState('')
  const [user, setUser] = useState('')
  const [days, setDays] = useState(1)
  const [limit, setLimit] = useState(500)
  const [autoRefresh, setAutoRefresh] = useState(0)

  const [rows, setRows] = useState<LogRow[]>([])
  const [kinds, setKinds] = useState<string[]>([])
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [layout, setLayout] = useState(() => ({ kind, columns: loadColumns(kind) }))
  const [picking, setPicking] = useState(false)
  if (layout.kind !== kind) setLayout({ kind, columns: loadColumns(kind) })
  const columns = layout.kind === kind ? layout.columns : loadColumns(kind)
  const setColumns = (next: SearchColumn[]) => {
    setLayout({ kind, columns: next })
    saveColumns(kind, next)
  }
  const shown = columns.filter((c) => c.visible)

  const query = useMemo(
    () => ({ platform, kind, text, account, user, days, limit }),
    [platform, kind, text, account, user, days, limit],
  )

  const inFlight = useRef<AbortController | null>(null)
  const run = useCallback(async () => {
    inFlight.current?.abort()
    const controller = new AbortController()
    inFlight.current = controller
    setBusy(true)
    try {
      const res = await search(query, controller.signal)
      if (controller.signal.aborted) return
      setRows(res.rows ?? [])
      setKinds(res.kinds ?? [])
      setError(null)
    } catch (err) {
      if (controller.signal.aborted) return
      setError(err instanceof Error ? err.message : 'unknown error')
    } finally {
      if (inFlight.current === controller) setBusy(false)
    }
  }, [query])

  useEffect(() => {
    const handle = globalThis.setTimeout(() => void run(), TYPING_PAUSE_MS)
    return () => globalThis.clearTimeout(handle)
  }, [run])

  useEffect(() => () => inFlight.current?.abort(), [])

  useEffect(() => {
    if (autoRefresh <= 0) return
    const handle = globalThis.setInterval(() => void run(), autoRefresh * 1000)
    return () => globalThis.clearInterval(handle)
  }, [autoRefresh, run])

  const time = useMemo(
    () => new Intl.DateTimeFormat(i18n.language, { dateStyle: 'short', timeStyle: 'medium' }),
    [i18n.language],
  )

  return (
    <Stack spacing={2}>
      {error ? <Alert severity="error">{error}</Alert> : null}

      <Stack direction="row" spacing={1} alignItems="center" flexWrap="wrap" useFlexGap>
        <FormControl size="small" sx={{ minWidth: 150 }}>
          <InputLabel id="search-kind">{t('search.kind')}</InputLabel>
          <Select
            labelId="search-kind"
            label={t('search.kind')}
            value={kind}
            onChange={(e) => setKind(String(e.target.value))}
          >
            <MenuItem value="">{t('search.anyKind')}</MenuItem>
            {kinds.map((k) => (
              <MenuItem key={k} value={k}>
                {k}
              </MenuItem>
            ))}
          </Select>
        </FormControl>

        <TextField
          size="small"
          label={t('search.text')}
          value={text}
          onChange={(e) => setText(e.target.value)}
          helperText={t('search.textHelp')}
          sx={{ minWidth: 240 }}
        />
        <TextField
          size="small"
          label={t('search.account')}
          value={account}
          onChange={(e) => setAccount(e.target.value)}
        />
        <TextField
          size="small"
          label={t('search.user')}
          value={user}
          onChange={(e) => setUser(e.target.value)}
        />

        <FormControl size="small" sx={{ minWidth: 130 }}>
          <InputLabel id="search-days">{t('search.days')}</InputLabel>
          <Select
            labelId="search-days"
            label={t('search.days')}
            value={days}
            onChange={(e) => setDays(Number(e.target.value))}
          >
            {DAY_CHOICES.map((d) => (
              <MenuItem key={d} value={d}>
                {t('search.dayCount', { count: d })}
              </MenuItem>
            ))}
          </Select>
        </FormControl>

        <FormControl size="small" sx={{ minWidth: 120 }}>
          <InputLabel id="search-limit">{t('search.limit')}</InputLabel>
          <Select
            labelId="search-limit"
            label={t('search.limit')}
            value={limit}
            onChange={(e) => setLimit(Number(e.target.value))}
          >
            {LIMITS.map((n) => (
              <MenuItem key={n} value={n}>
                {n}
              </MenuItem>
            ))}
          </Select>
        </FormControl>

        <FormControl size="small" sx={{ minWidth: 150 }}>
          <InputLabel id="search-refresh">{t('search.autoRefresh')}</InputLabel>
          <Select
            labelId="search-refresh"
            label={t('search.autoRefresh')}
            value={autoRefresh}
            onChange={(e) => setAutoRefresh(Number(e.target.value))}
          >
            <MenuItem value={0}>{t('search.off')}</MenuItem>
            <MenuItem value={10}>{t('search.everySeconds', { count: 10 })}</MenuItem>
            <MenuItem value={60}>{t('search.everySeconds', { count: 60 })}</MenuItem>
          </Select>
        </FormControl>

        <Button size="small" startIcon={<SearchIcon />} disabled={busy} onClick={() => void run()}>
          {t('search.run')}
        </Button>
        <Button size="small" startIcon={<DownloadIcon />} href={searchCsvUrl(query)}>
          {t('search.export')}
        </Button>
        <Button size="small" startIcon={<ViewColumnIcon />} onClick={() => setPicking(true)}>
          {t('search.columns')}
        </Button>
      </Stack>

      {picking ? (
        <ColumnsDialog
          columns={columns}
          found={fieldsFound(rows, columns)}
          onChange={setColumns}
          onClose={() => setPicking(false)}
        />
      ) : null}

      {busy ? <Busy bar label={t('search.searching')} /> : null}
      <Typography variant="caption" color="text.secondary">
        {busy ? t('search.searching') : t('search.found', { count: rows.length })}
      </Typography>

      <Box sx={{ overflowX: 'auto' }}>
        <Table size="small">
          <TableHead>
            <TableRow>
              <TableCell align="right" sx={{ width: 48 }}>
                {t('grid.number')}
              </TableCell>
              {shown.map((c) => (
                <TableCell key={c.id}>{fieldOf(c.id) ?? t(`search.column.${c.id}`)}</TableCell>
              ))}
            </TableRow>
          </TableHead>
          <TableBody className={PAIRED_ROWS}>
            {rows.map((row, i) => (
              <LogRowView
                key={`${row.ts}-${i}`}
                number={i + 1}
                row={row}
                columns={shown}
                when={time.format(new Date(row.ts))}
              />
            ))}
          </TableBody>
        </Table>
      </Box>
    </Stack>
  )
}

function LogRowView({
  number,
  row,
  columns,
  when,
}: {
  number: number
  row: LogRow
  columns: SearchColumn[]
  when: string
}) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)

  return (
    <>
      <TableRow hover sx={{ cursor: 'pointer' }} onClick={() => setOpen(!open)}>
        <TableCell align="right" sx={{ color: 'text.secondary' }}>
          {number}
        </TableCell>
        {columns.map((c) => (
          <Cell key={c.id} id={c.id} row={row} when={when} />
        ))}
      </TableRow>
      <TableRow>
        <TableCell colSpan={columns.length + 1} sx={{ py: 0, border: 0 }}>
          <Collapse in={open} unmountOnExit>
            <Box
              component="pre"
              sx={{ m: 0, p: 1, overflowX: 'auto', fontSize: 12, whiteSpace: 'pre-wrap' }}
            >
              {JSON.stringify(row.payload ?? {}, null, 2)}
            </Box>
            <IconButton size="small" sx={{ display: 'none' }} aria-label={t('search.close')} />
          </Collapse>
        </TableCell>
      </TableRow>
    </>
  )
}

function Cell({ id, row, when }: { id: string; row: LogRow; when: string }) {
  const field = fieldOf(id)
  if (field !== null) {
    return (
      <TableCell>
        <Typography variant="body2" noWrap sx={{ maxWidth: 320 }} title={fieldValue(row, field)}>
          {fieldValue(row, field)}
        </Typography>
      </TableCell>
    )
  }
  switch (id) {
    case 'when':
      return <TableCell sx={{ whiteSpace: 'nowrap' }}>{when}</TableCell>
    case 'kind':
      return <TableCell>{row.signal}</TableCell>
    case 'account':
      return <TableCell>{row.account ?? ''}</TableCell>
    case 'user':
      return <TableCell>{row.user ?? ''}</TableCell>
    case 'url':
      return <TableCell>{row.url ?? ''}</TableCell>
    case 'level':
      return <TableCell>{row.level ?? ''}</TableCell>
    default:
      return (
        <TableCell>
          <Typography variant="body2" noWrap sx={{ maxWidth: 520 }}>
            {summarise(row)}
          </Typography>
        </TableCell>
      )
  }
}

/** One line of the row's own fields, so the table is readable without opening it. */
function summarise(row: LogRow): string {
  const payload = row.payload ?? {}
  const interesting = ['subject', 'to', 'url', 'emailType', 'executionTime', 'message']
  const parts: string[] = []
  for (const key of interesting) {
    const value = payload[key]
    if (value !== undefined && value !== null && value !== '') {
      parts.push(`${key}: ${String(value)}`)
    }
  }
  if (parts.length === 0) {
    parts.push(
      ...Object.entries(payload)
        .slice(0, 3)
        .map(([k, v]) => `${k}: ${String(v)}`),
    )
  }
  return parts.join(' · ')
}
