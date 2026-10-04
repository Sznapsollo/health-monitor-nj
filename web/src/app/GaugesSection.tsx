import Stack from '@mui/material/Stack'
import Typography from '@mui/material/Typography'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Busy } from './Busy'

import type { SignalSpec } from '../api/catalogue'
import { fetchGauges, type GaugeView } from '../api/session'
import { BarGauge } from '../charts/BarGauge'
import { gaugeSortOf } from '../charts/gaugeSort'
import { ChartTile } from '../charts/ChartTile'
import { everyVisible } from './everyVisible'

interface Props {
  platform: string
  /** The gauges to show, in the order to show them, with their display names. */
  specs?: SignalSpec[]
  onMove?: (signal: string, direction: 1 | -1) => void
  /** The wall display keeps refreshing even when the browser says it is hidden. */
  alwaysPoll?: boolean
}

/** Gauges refresh on their own: they carry no history to merge. */
export function GaugesSection({ platform, specs = [], onMove, alwaysPoll = false }: Props) {
  const { t } = useTranslation()
  const [gauges, setGauges] = useState<GaugeView[]>([])
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    if (!platform) return
    let cancelled = false
    const load = () => {
      fetchGauges(platform)
        .then((list) => {
          if (!cancelled) {
            setGauges(list)
            setLoading(false)
          }
        })
        .catch(() => {
          if (!cancelled) {
            setGauges([])
            setLoading(false)
          }
        })
    }
    load()
    const handle = alwaysPoll ? globalThis.setInterval(load, 10_000) : undefined
    const stop = alwaysPoll ? undefined : everyVisible(load, 10_000)
    return () => {
      cancelled = true
      globalThis.clearInterval(handle)
      stop?.()
    }
  }, [platform, alwaysPoll])

  const picked = specs.flatMap((spec) => {
    const gauge = gauges.find((g) => g.signal === spec.name)
    return gauge ? [{ spec, gauge }] : []
  })
  if (loading) return <Busy />
  if (picked.length === 0) return null

  return (
    <Stack spacing={2}>
      <Typography variant="h6" component="h2">
        {t('gauges.title')}
      </Typography>
      <Stack
        direction="row"
        spacing={2}
        flexWrap="wrap"
        useFlexGap
        sx={{ '& > *': { flex: '1 1 320px', minWidth: 0 } }}
      >
        {picked.map(({ spec, gauge: g }) => (
          <ChartTile
            key={g.signal}
            title={spec.displayName || g.signal}
            subtitle={t('gauges.from', { sources: g.sources.join(', ') })}
            onMove={onMove && picked.length > 1 ? (d) => onMove(g.signal, d) : undefined}
          >
            <BarGauge points={g.points ?? []} sort={gaugeSortOf(spec)} />
          </ChartTile>
        ))}
      </Stack>
    </Stack>
  )
}
