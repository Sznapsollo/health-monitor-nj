import Box from '@mui/material/Box'
import { memo, useEffect, useRef } from 'react'
import { useTranslation } from 'react-i18next'

import { useThemeMode } from '../theme/useThemeMode'
import { init, type ECharts } from './echarts'
import { minuteSeriesOption, type SeriesSpec } from './options'
import { useMinuteFormatter } from './useMinuteFormatter'

interface Props {
  series: SeriesSpec[]
  height?: number
  showLegend?: boolean
}

/**
 * One ECharts instance per tile, reused across updates and disposed with the
 * tile — a wall display keeps these alive for weeks.
 */
export const MinuteSeriesChart = memo(function MinuteSeriesChart({
  series,
  height = 240,
  showLegend = false,
}: Props) {
  const { t } = useTranslation()
  const { resolved } = useThemeMode()
  const formatMinute = useMinuteFormatter()
  const host = useRef<HTMLDivElement>(null)
  const chart = useRef<ECharts | null>(null)
  const drawnWith = useRef<unknown[] | null>(null)

  useEffect(() => {
    if (!host.current) return
    chart.current = init(host.current, undefined, { renderer: 'canvas' })
    const onResize = () => chart.current?.resize()
    globalThis.addEventListener('resize', onResize)
    const observer = typeof ResizeObserver === 'undefined' ? null : new ResizeObserver(onResize)
    observer?.observe(host.current)
    return () => {
      observer?.disconnect()
      globalThis.removeEventListener('resize', onResize)
      chart.current?.dispose()
      chart.current = null
    }
  }, [])

  useEffect(() => {
    // The palette is redrawn on a theme change because a canvas inherits
    // nothing from CSS. New data alone only replaces the series,
    // which is cheaper and keeps where the viewer has zoomed to.
    const style = [resolved, formatMinute, showLegend, t]
    const restyle = !drawnWith.current || style.some((v, i) => v !== drawnWith.current?.[i])
    drawnWith.current = style
    chart.current?.setOption(
      minuteSeriesOption({
        series,
        mode: resolved,
        formatMinute,
        showLegend,
        countLabel: t('chart.count'),
        latencyLabel: t('chart.latency'),
      }),
      restyle ? { notMerge: true } : { replaceMerge: ['series', 'yAxis'] },
    )
  }, [series, resolved, formatMinute, showLegend, t])

  return <Box ref={host} sx={{ width: '100%', height }} />
})
