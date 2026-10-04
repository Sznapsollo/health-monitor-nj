import OpenInNewIcon from '@mui/icons-material/OpenInNew'
import Alert from '@mui/material/Alert'
import Box from '@mui/material/Box'
import IconButton from '@mui/material/IconButton'
import Tooltip from '@mui/material/Tooltip'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import type { Level } from '../api/alerts'
import type { Panel } from '../api/dashboards'
import type { SignalSpec } from '../api/catalogue'
import { AlertsPanel } from '../alerts/AlertsPanel'
import { StatusTable } from '../alerts/StatusTable'
import { BarGauge } from '../charts/BarGauge'
import { gaugeSortOf } from '../charts/gaugeSort'
import { ChartTile } from '../charts/ChartTile'
import { MinuteSeriesChart } from '../charts/MinuteSeriesChart'
import {
  groupName,
  showMainOf,
  subStyleOf,
  togetherSeries,
  type SeriesSpec,
} from '../charts/options'
import { useGroupTiles } from '../charts/useGroupTiles'
import { useMonitorStore } from '../store/useMonitorStore'
import { PanelFilter } from './PanelFilter'
import type { GaugeView } from '../api/session'

/** The tab a panel is a window onto, so its header can send you there. */
export type PanelTab = 'charts' | 'alerts' | 'status'

const TAB_OF: Record<Panel['type'], PanelTab> = {
  chart: 'charts',
  gauge: 'charts',
  alerts: 'alerts',
  status: 'status',
}

interface Props {
  panel: Panel
  /** The key its chart data is stored under. */
  viewId: string
  platform: string
  specs: SignalSpec[]
  gauges: GaugeView[]
  onOpen?: (tab: PanelTab) => void
  /** Offers a filter box on grouped charts; wall displays leave it out. */
  filterable?: boolean
}

/** One box on a dashboard, drawn by whichever component already knows how. */
export function DashboardPanel({
  panel,
  viewId,
  platform,
  specs,
  gauges,
  onOpen,
  filterable,
}: Props) {
  const { t } = useTranslation()
  const view = useMonitorStore((s) => (panel.signal ? s.views[viewId] : undefined))
  const typed = useMonitorStore((s) => s.panelFilters[viewId])
  const filter = typed ?? panel.filter ?? ''
  const spec = specs.find((s) => s.name === panel.signal)
  const title = panel.title || spec?.displayName || panel.signal || t(`dashboard.${panel.type}`)
  const height = panel.height ?? 240

  // The panel is a slice of a full tab; this takes you to the whole of it.
  const tab = TAB_OF[panel.type]
  const openLabel = t('dashboard.open', { tab: t(`tabs.${tab}`) })
  const filterBox =
    filterable && panel.type === 'chart' && panel.group ? (
      <PanelFilter viewId={viewId} saved={panel.filter ?? ''} />
    ) : null
  const actions = (
    <>
      {filterBox}
      {onOpen ? (
        <Tooltip title={openLabel}>
          <IconButton size="small" onClick={() => onOpen(tab)} aria-label={openLabel}>
            <OpenInNewIcon fontSize="small" />
          </IconButton>
        </Tooltip>
      ) : null}
    </>
  )

  const series: SeriesSpec[] = useMemo(() => {
    const total = view?.total ?? []
    return [
      { name: t('chart.count'), points: total, value: 'count', style: 'column' },
      { name: t('chart.latency'), points: total, value: 'avgMs', style: 'line', secondary: true },
    ]
  }, [view?.total, t])

  const groups = view?.groups
  const shown = useMemo(() => (groups ?? []).slice(0, panel.top ?? 4), [groups, panel.top])
  const tiles = useGroupTiles(
    panel.together ? [] : shown,
    subStyleOf(panel),
    t('chart.rest'),
    spec?.colors,
  )
  const together = useMemo(
    () => (panel.together ? togetherSeries(shown, t('chart.rest'), spec?.colors) : []),
    [panel.together, shown, t, spec?.colors],
  )

  switch (panel.type) {
    case 'chart': {
      return (
        <ChartTile
          title={title}
          subtitle={view?.filtered ? t('chart.filteredBy', { filter }) : undefined}
          actions={actions}
        >
          {view ? (
            <>
              {showMainOf(panel) ? (
                <MinuteSeriesChart series={series} showLegend height={height} />
              ) : null}
              {together.length > 0 ? (
                <Box sx={{ mt: showMainOf(panel) ? 1 : 0 }}>
                  <MinuteSeriesChart series={together} showLegend height={height} />
                </Box>
              ) : null}
              {tiles.length > 0 ? (
                <Box
                  sx={{
                    display: 'grid',
                    gap: 1,
                    gridTemplateColumns: '1fr 1fr',
                    mt: showMainOf(panel) ? 1 : 0,
                  }}
                >
                  {tiles.map(({ group, series }) => (
                    <Box key={group.value}>
                      <Box
                        component="span"
                        sx={{ fontSize: 12, opacity: 0.7, display: 'block' }}
                        title={group.value}
                      >
                        {groupName(group)}
                      </Box>
                      <MinuteSeriesChart
                        series={series}
                        height={Math.max(110, Math.round(height * 0.55))}
                      />
                    </Box>
                  ))}
                </Box>
              ) : null}
            </>
          ) : (
            <Alert severity="info">{t('chart.waiting')}</Alert>
          )}
        </ChartTile>
      )
    }

    case 'gauge': {
      const gauge = gauges.find((g) => g.signal === panel.signal)
      return (
        <ChartTile
          title={title}
          subtitle={gauge ? t('gauges.from', { sources: gauge.sources.join(', ') }) : undefined}
          actions={actions}
        >
          {gauge ? (
            <BarGauge
              points={gauge.points ?? []}
              sort={gaugeSortOf(
                specs.find((s) => s.name === panel.signal),
                panel.sort,
              )}
            />
          ) : (
            <Alert severity="info">{t('chart.waiting')}</Alert>
          )}
        </ChartTile>
      )
    }

    case 'alerts':
      return (
        <ChartTile title={panel.title || t('dashboard.alerts')} actions={actions}>
          <Box sx={{ maxHeight: height * 1.6, overflow: 'auto' }}>
            <AlertsPanel
              platform={platform}
              compact
              levels={panel.levels as Level[] | undefined}
              categories={panel.categories}
              limit={panel.limit}
            />
          </Box>
        </ChartTile>
      )

    case 'status':
      return (
        <ChartTile title={panel.title || t('dashboard.status')} actions={actions}>
          <Box sx={{ maxHeight: height * 1.6, overflow: 'auto' }}>
            <StatusTable platform={platform} compact />
          </Box>
        </ChartTile>
      )
  }
}
