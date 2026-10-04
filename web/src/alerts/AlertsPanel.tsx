import DownloadIcon from '@mui/icons-material/Download'
import NotificationsOffIcon from '@mui/icons-material/NotificationsOff'
import MuiAlert from '@mui/material/Alert'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Checkbox from '@mui/material/Checkbox'
import Chip from '@mui/material/Chip'
import FormControlLabel from '@mui/material/FormControlLabel'
import IconButton from '@mui/material/IconButton'
import MenuItem from '@mui/material/MenuItem'
import Select from '@mui/material/Select'
import Stack from '@mui/material/Stack'
import Table from '@mui/material/Table'
import TableBody from '@mui/material/TableBody'
import TableCell from '@mui/material/TableCell'
import TableHead from '@mui/material/TableHead'
import TablePagination from '@mui/material/TablePagination'
import TableRow from '@mui/material/TableRow'
import TextField from '@mui/material/TextField'
import Tooltip from '@mui/material/Tooltip'
import Typography from '@mui/material/Typography'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Busy } from '../app/Busy'

import { alertsCsvUrl, type Alert, type Level } from '../api/alerts'
import { applyFilters, ALERTS_LIMIT, useAlertStore } from '../store/useAlertStore'
import { SilenceDialog } from './SilenceDialog'
import { silenceMessageOf } from './silenceTargets'

const LEVELS: Level[] = ['ERROR', 'WARN', 'INFO']

const PAGE_SIZES = [25, 50, 100, 250]

const LEVEL_COLOR: Record<Level, 'error' | 'warning' | 'info' | 'default'> = {
  ERROR: 'error',
  WARN: 'warning',
  INFO: 'info',
  DEBUG: 'default',
  TRACE: 'default',
}

/** 20260919 reads as 2026-09-19; the picker is for people, not for machines. */
function dayLabel(day: string): string {
  if (day.length !== 8) return day
  return `${day.slice(0, 4)}-${day.slice(4, 6)}-${day.slice(6)}`
}

interface Props {
  platform: string
  /** Inside a dashboard panel: the rows only, without the filter bar. */
  compact?: boolean
  /**
   * What a dashboard panel asks to see. A panel is an arrangement someone
   * wrote down, so its own levels, categories and limit apply instead of the
   * filters the Alerts tab happens to have on.
   */
  levels?: Level[]
  categories?: string[]
  limit?: number
}

export function AlertsPanel({
  platform,
  compact = false,
  levels: panelLevels,
  categories: panelCategories,
  limit,
}: Props) {
  const { t, i18n } = useTranslation()
  const { alerts, categories, filters, setFilters, error, loading } = useAlertStore()
  const [silenceFor, setSilenceFor] = useState<Alert | null>(null)
  const loadSilences = useAlertStore((s) => s.loadSilences)
  const historyDay = useAlertStore((s) => s.historyDay)
  const historyDays = useAlertStore((s) => s.historyDays)
  const loadHistory = useAlertStore((s) => s.loadHistory)
  const loadLive = useAlertStore((s) => s.load)

  const shown = useMemo(() => {
    const asked =
      panelLevels?.length || panelCategories?.length
        ? {
            levels: panelLevels ?? [],
            categories: panelCategories ?? [],
            text: '',
            minMs: 0,
            silenced: false,
          }
        : filters
    const list = applyFilters(alerts, asked)
    return limit && limit > 0 ? list.slice(0, limit) : list
  }, [alerts, filters, panelLevels, panelCategories, limit])
  // A new filter or day starts at the first page; a live update keeps the page.
  const pagingKey = JSON.stringify(filters) + (historyDay ?? '')
  const [paging, setPaging] = useState({ key: pagingKey, page: 0, size: 50 })
  const lastPage = Math.max(0, Math.ceil(shown.length / paging.size) - 1)
  const page = paging.key === pagingKey ? Math.min(paging.page, lastPage) : 0
  const rows = compact ? shown : shown.slice(page * paging.size, (page + 1) * paging.size)

  const time = useMemo(
    () => new Intl.DateTimeFormat(i18n.language, { dateStyle: 'short', timeStyle: 'medium' }),
    [i18n.language],
  )

  const toggleLevel = (level: Level) => {
    const levels = filters.levels.includes(level)
      ? filters.levels.filter((l) => l !== level)
      : [...filters.levels, level]
    setFilters({ levels })
  }

  const table = (
    <Box sx={{ overflowX: 'auto' }}>
      <Table size="small" stickyHeader>
        <TableHead>
          <TableRow>
            <TableCell align="right" sx={{ width: 48 }}>
              {t('grid.number')}
            </TableCell>
            <TableCell>{t('alerts.when')}</TableCell>
            <TableCell>{t('alerts.level')}</TableCell>
            {compact ? null : <TableCell>{t('alerts.category')}</TableCell>}
            <TableCell align="right">{t('alerts.count')}</TableCell>
            <TableCell>{t('alerts.message')}</TableCell>
            <TableCell align="right" />
          </TableRow>
        </TableHead>
        <TableBody>
          {rows.map((a, i) => (
            <AlertRow
              key={a.id}
              number={(compact ? 0 : page * paging.size) + i + 1}
              alert={a}
              when={time.format(new Date(a.last))}
              compact={compact}
              onSilence={() => setSilenceFor(a)}
            />
          ))}
        </TableBody>
      </Table>
    </Box>
  )

  if (compact) {
    return (
      <>
        {table}
        <SilenceDialog
          key={silenceFor?.id}
          open={silenceFor !== null}
          platform={platform}
          message={silenceFor ? silenceMessageOf(silenceFor) : ''}
          category={silenceFor?.category ?? ''}
          categories={categories}
          onClose={() => setSilenceFor(null)}
          onCreated={() => {
            void loadSilences(platform)
            if (!historyDay) void loadLive(platform)
          }}
        />
      </>
    )
  }

  return (
    <Stack spacing={2}>
      {error ? <MuiAlert severity="error">{error}</MuiAlert> : null}
      {loading ? <Busy bar /> : null}

      <Stack direction="row" spacing={1} alignItems="center" flexWrap="wrap" useFlexGap>
        {LEVELS.map((level) => (
          <Chip
            key={level}
            label={level}
            size="small"
            // The row badges carry the same words, so the filter chips say
            // what they are for.
            aria-label={t('alerts.filterByLevel', { level })}
            color={filters.levels.includes(level) ? LEVEL_COLOR[level] : 'default'}
            variant={filters.levels.includes(level) ? 'filled' : 'outlined'}
            onClick={() => toggleLevel(level)}
          />
        ))}

        <TextField
          size="small"
          label={t('alerts.search')}
          value={filters.text}
          onChange={(e) => setFilters({ text: e.target.value })}
          sx={{ minWidth: 220 }}
        />
        <TextField
          size="small"
          type="number"
          label={t('alerts.minMs')}
          value={filters.minMs || ''}
          onChange={(e) => setFilters({ minMs: Number(e.target.value) })}
          sx={{ width: 140 }}
        />
        <FormControlLabel
          control={
            <Checkbox
              size="small"
              checked={filters.silenced}
              onChange={(e) => setFilters({ silenced: e.target.checked })}
            />
          }
          label={t('alerts.showSilenced')}
        />
        <Button
          size="small"
          startIcon={<DownloadIcon />}
          href={alertsCsvUrl({
            platform,
            silenced: filters.silenced,
            day: historyDay ?? undefined,
          })}
        >
          {t('alerts.export')}
        </Button>

        {/* Alerts outlive the memory they live in; this is how you look back. */}
        <Select
          size="small"
          value={historyDay ?? ''}
          onChange={(e) => {
            const day = String(e.target.value)
            if (day === '') void loadLive(platform)
            else void loadHistory(platform, day)
          }}
          sx={{ minWidth: 150 }}
          inputProps={{ 'aria-label': t('alerts.day') }}
        >
          <MenuItem value="">{t('alerts.live')}</MenuItem>
          {historyDays.map((day) => (
            <MenuItem key={day} value={day}>
              {dayLabel(day)}
            </MenuItem>
          ))}
        </Select>
      </Stack>

      {historyDay ? (
        <MuiAlert severity="info">
          {t('alerts.showingHistory', { day: dayLabel(historyDay) })}
          {alerts.length >= ALERTS_LIMIT
            ? ` ${t('alerts.historyCapped', { limit: ALERTS_LIMIT.toLocaleString() })}`
            : ''}
        </MuiAlert>
      ) : null}

      {categories.length > 0 ? (
        <Stack direction="row" spacing={1} flexWrap="wrap" useFlexGap>
          {categories.map((category) => (
            <Chip
              key={category}
              label={category}
              size="small"
              aria-label={t('alerts.filterByCategory', { category })}
              variant={filters.categories.includes(category) ? 'filled' : 'outlined'}
              onClick={() =>
                setFilters({
                  categories: filters.categories.includes(category)
                    ? filters.categories.filter((c) => c !== category)
                    : [...filters.categories, category],
                })
              }
            />
          ))}
        </Stack>
      ) : null}

      <Typography variant="caption" color="text.secondary">
        {t('alerts.showing', { shown: shown.length, total: alerts.length })}
      </Typography>

      {table}

      <TablePagination
        component="div"
        count={shown.length}
        page={page}
        rowsPerPage={paging.size}
        rowsPerPageOptions={PAGE_SIZES}
        onPageChange={(_, next) => setPaging({ key: pagingKey, page: next, size: paging.size })}
        onRowsPerPageChange={(e) =>
          setPaging({ key: pagingKey, page: 0, size: Number(e.target.value) })
        }
        labelRowsPerPage={t('alerts.perPage')}
        labelDisplayedRows={({ from, to, count }) => t('alerts.pageOf', { from, to, count })}
      />

      <SilenceDialog
        key={silenceFor?.id}
        open={silenceFor !== null}
        platform={platform}
        message={silenceFor ? silenceMessageOf(silenceFor) : ''}
        category={silenceFor?.category ?? ''}
        categories={categories}
        onClose={() => setSilenceFor(null)}
        onCreated={() => {
          void loadSilences(platform)
          if (!historyDay) void loadLive(platform)
        }}
      />
    </Stack>
  )
}

function AlertRow({
  number,
  alert,
  when,
  compact,
  onSilence,
}: {
  number: number
  alert: Alert
  when: string
  compact?: boolean
  onSilence: () => void
}) {
  const { t } = useTranslation()
  return (
    <TableRow sx={alert.silenced ? { opacity: 0.55 } : undefined}>
      <TableCell align="right" sx={{ color: 'text.secondary' }}>
        {number}
      </TableCell>
      <TableCell sx={{ whiteSpace: 'nowrap' }}>{when}</TableCell>
      <TableCell>
        <Chip size="small" label={alert.level} color={LEVEL_COLOR[alert.level]} />
      </TableCell>
      {compact ? null : <TableCell>{alert.category ?? ''}</TableCell>}
      <TableCell align="right">{alert.count > 1 ? alert.count : ''}</TableCell>
      <TableCell>
        <Typography variant="body2">{alert.message}</Typography>
        {alert.silenced ? (
          <Typography variant="caption" color="text.secondary">
            {t('alerts.silencedBecause', { reason: alert.silenceReason })}
          </Typography>
        ) : null}
      </TableCell>
      <TableCell align="right">
        <Tooltip title={t('alerts.silence')}>
          <IconButton size="small" onClick={onSilence} aria-label={t('alerts.silence')}>
            <NotificationsOffIcon fontSize="small" />
          </IconButton>
        </Tooltip>
      </TableCell>
    </TableRow>
  )
}
