import Alert from '@mui/material/Alert'
import Box from '@mui/material/Box'
import Paper from '@mui/material/Paper'
import Stack from '@mui/material/Stack'
import Typography from '@mui/material/Typography'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Busy } from './Busy'

import { apiFetch } from '../api/http'
import { gigabytes, megabytes, perSecond, strained } from '../api/server'
import { Sparkline } from '../charts/Sparkline'
import { useServerStore } from '../store/useServerStore'
import { everyVisible } from './everyVisible'

interface WriterStats {
  written?: number
  dropped?: number
  sampled?: number
  errors?: number
  queued?: number
  pending?: number
}

interface ServerState {
  intake?: Record<string, number>
  writer?: WriterStats
  logWriter?: WriterStats
  alertWriter?: WriterStats
  statusWriter?: WriterStats
  hotBytes?: number
  hotEvictions?: number
  durableRows?: Record<string, number>
}

/** What the monitor itself is doing and costing, on the Status tab. */
export function ServerStats() {
  const { t } = useTranslation()
  const [state, setState] = useState<ServerState | null>(null)
  const [error, setError] = useState<string | null>(null)
  const { resources, load: loadServer } = useServerStore()

  useEffect(() => {
    let cancelled = false
    const load = async () => {
      try {
        const res = await apiFetch('/api/state')
        if (!res.ok) throw new Error(`/api/state responded ${res.status}`)
        const body = (await res.json()) as ServerState
        if (!cancelled) {
          setState(body)
          setError(null)
        }
      } catch (err) {
        if (!cancelled) setError(err instanceof Error ? err.message : 'unknown error')
      }
    }
    const both = () => {
      void load()
      void loadServer(true)
    }
    both()
    const stop = everyVisible(both, 10_000)
    return () => {
      cancelled = true
      stop()
    }
  }, [loadServer])

  const writers = (
    [
      ['writer', t('server.writers.aggregates')],
      ['logWriter', t('server.writers.logs')],
      ['alertWriter', t('server.writers.alerts')],
      ['statusWriter', t('server.writers.status')],
    ] as const
  ).flatMap(([key, title]) => {
    const stats = state?.[key]
    return stats ? [{ key, title, stats }] : []
  })

  return (
    <Stack spacing={2}>
      {error ? <Alert severity="error">{error}</Alert> : null}
      {!resources && !state && !error ? <Busy /> : null}
      {resources ? (
        <Box>
          <Typography variant="subtitle1" gutterBottom>
            {t('server.title')}
          </Typography>
          <Stack direction={{ xs: 'column', md: 'row' }} spacing={2}>
            <Paper variant="outlined" sx={{ p: 2, flex: 1 }}>
              <Typography variant="subtitle2">{t('server.memory')}</Typography>
              <Typography
                variant="h5"
                color={strained(resources).memory ? 'warning.main' : undefined}
              >
                {megabytes(resources.rssBytes)}
              </Typography>
              <Typography variant="body2" color="text.secondary">
                {t('server.memoryDetail', {
                  limit: resources.memLimitBytes
                    ? t('server.limit', { limit: megabytes(resources.memLimitBytes) })
                    : t('server.noLimit'),
                  heap: megabytes(resources.heapBytes),
                })}
              </Typography>
              <Sparkline
                label={t('server.memoryTrend')}
                values={(resources.history ?? []).map((h) => h.rssBytes)}
              />
            </Paper>
            <Paper variant="outlined" sx={{ p: 2, flex: 1 }}>
              <Typography variant="subtitle2">{t('server.cpu')}</Typography>
              <Typography variant="h5" color={strained(resources).cpu ? 'warning.main' : undefined}>
                {t('server.percent', { value: resources.cpuPercent1m })}
              </Typography>
              <Typography variant="body2" color="text.secondary">
                {t('server.cpuDetail', {
                  now: resources.cpuPercent,
                  cores: t('server.cores', { count: resources.cores }),
                })}
              </Typography>
              <Sparkline
                label={t('server.cpuTrend')}
                values={(resources.history ?? []).map((h) => h.cpuPercent)}
              />
            </Paper>
            {resources.packetsPerSec !== undefined ? (
              <Paper variant="outlined" sx={{ p: 2, flex: 1 }}>
                <Typography variant="subtitle2">{t('server.packets')}</Typography>
                <Typography variant="h5">
                  {resources.packetsLastMinute !== undefined
                    ? t('server.packetsCount', {
                        count: resources.packetsLastMinute.toLocaleString(),
                      })
                    : '…'}
                </Typography>
                <Typography variant="body2" color="text.secondary">
                  {t('server.packetsDetail', { now: perSecond(resources.packetsPerSec) })}
                </Typography>
                <Sparkline
                  label={t('server.packetsTrend')}
                  values={(resources.history ?? []).map((h) => h.packetsPerSec ?? 0)}
                />
              </Paper>
            ) : null}
            {resources.logsLastMinute !== undefined ? (
              <Paper variant="outlined" sx={{ p: 2, flex: 1 }}>
                <Typography variant="subtitle2">{t('server.logs')}</Typography>
                <Typography variant="h5">
                  {t('server.logsCount', { count: resources.logsLastMinute.toLocaleString() })}
                </Typography>
                <Typography variant="body2" color="text.secondary">
                  {t('server.logsDetail')}
                </Typography>
              </Paper>
            ) : null}
            {(resources.disks ?? []).slice(0, 1).map((d) => (
              <Paper key="disk" variant="outlined" sx={{ p: 2, flex: 1 }}>
                <Typography variant="subtitle2">{t('server.disk')}</Typography>
                <Typography variant="h5" color={d.low ? 'error' : undefined}>
                  {t('server.diskFree', { free: gigabytes(d.freeBytes) })}
                </Typography>
                <Typography variant="body2" color="text.secondary">
                  {t('server.diskDetail', {
                    total: gigabytes(d.totalBytes),
                    folders: d.folders.map((f) => t(`storage.folder.${f}`)).join(', '),
                  })}
                </Typography>
              </Paper>
            ))}
            {resources.dbBytes ? (
              <Paper variant="outlined" sx={{ p: 2, flex: 1 }}>
                <Typography variant="subtitle2">{t('server.databaseSize')}</Typography>
                <Typography variant="h5">{megabytes(resources.dbBytes)}</Typography>
                <Typography variant="body2" color="text.secondary">
                  {t('server.databaseDetail')}
                </Typography>
              </Paper>
            ) : null}
            <Counters
              title={t('server.process')}
              values={{
                [t('server.goroutines')]: resources.goroutines,
                [t('server.openFiles')]: resources.openFiles ?? 0,
                [t('server.gcCycles')]: resources.gcCycles,
                [t('server.uptimeHours')]: Math.round(resources.uptimeSeconds / 360) / 10,
              }}
            />
          </Stack>
        </Box>
      ) : null}

      {state ? (
        <Stack direction={{ xs: 'column', md: 'row' }} spacing={2} flexWrap="wrap" useFlexGap>
          <Counters title={t('health.intake')} values={state.intake ?? {}} />
          <Counters
            title={t('server.storage')}
            values={{
              [t('server.hotState')]: Math.round(Number(state.hotBytes ?? 0) / 1024),
              [t('server.hotEvictions')]: Number(state.hotEvictions ?? 0),
              ...Object.fromEntries(
                Object.entries(state.durableRows ?? {}).map(([k, v]) => [
                  t('server.rows', { table: k }),
                  v,
                ]),
              ),
            }}
          />
          {writers.map((w) => (
            <Counters key={w.key} title={w.title} values={w.stats as Record<string, number>} />
          ))}
        </Stack>
      ) : null}
    </Stack>
  )
}

function Counters({ title, values }: { title: string; values: Record<string, number> }) {
  return (
    <Paper variant="outlined" sx={{ p: 2, flex: 1 }}>
      <Typography variant="subtitle2" gutterBottom>
        {title}
      </Typography>
      {Object.entries(values).map(([name, value]) => (
        <Stack key={name} direction="row" justifyContent="space-between">
          <Typography variant="body2" color="text.secondary">
            {name}
          </Typography>
          <Typography variant="body2">{Number(value ?? 0).toLocaleString()}</Typography>
        </Stack>
      ))}
    </Paper>
  )
}
