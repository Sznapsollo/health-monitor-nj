import ArrowDownwardIcon from '@mui/icons-material/ArrowDownward'
import ArrowUpwardIcon from '@mui/icons-material/ArrowUpward'
import Alert from '@mui/material/Alert'
import Box from '@mui/material/Box'
import Chip from '@mui/material/Chip'
import IconButton from '@mui/material/IconButton'
import Tooltip from '@mui/material/Tooltip'
import Stack from '@mui/material/Stack'
import Typography from '@mui/material/Typography'
import { memo, useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import type { SignalSpec } from '../api/catalogue'
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
import { CriteriaPanel } from '../criteria/CriteriaPanel'
import { useMonitorStore } from '../store/useMonitorStore'
import { applyOrder, useUiStore } from '../store/useUiStore'
import { OTHER_BUCKET, type Criteria, type SignalView } from '../ws/types'

interface Props {
  spec: SignalSpec
  criteria: Criteria
  view?: SignalView
  onMove?: (signal: string, direction: 1 | -1) => void
  first?: boolean
  last?: boolean
}

export const SignalSection = memo(function SignalSection({
  spec,
  criteria,
  view,
  onMove,
  first,
  last,
}: Props) {
  const { t } = useTranslation()
  const setCriteria = useMonitorStore((s) => s.setCriteria)
  const { tileHeight, columns, showLegend, minimised, order, toggleMinimised, move } = useUiStore()

  // The main chart is the old "Wszystko": counts as columns, average latency
  // as a line on the second axis.
  const mainSeries: SeriesSpec[] = useMemo(() => {
    const total = view?.total ?? []
    return [
      { name: t('chart.count'), points: total, value: 'count', style: 'column' },
      { name: t('chart.latency'), points: total, value: 'avgMs', style: 'line', secondary: true },
    ]
  }, [view?.total, t])

  const groups = useMemo(
    () => applyOrder(view?.groups ?? [], order[spec.name] ?? []),
    [view?.groups, order, spec.name],
  )
  const subStyle = subStyleOf(criteria)
  const tiles = useGroupTiles(
    criteria.together ? [] : groups,
    subStyle,
    t('chart.rest'),
    spec.colors,
  )
  const together = useMemo(
    () => (criteria.together ? togetherSeries(groups, t('chart.rest'), spec.colors) : []),
    [criteria.together, groups, t, spec.colors],
  )

  return (
    <Stack spacing={2}>
      <Stack direction="row" spacing={1} alignItems="center" flexWrap="wrap" useFlexGap>
        <Typography variant="h6" component="h2">
          {spec.displayName}
        </Typography>
        {onMove ? (
          <>
            <Tooltip title={t('picker.moveUp')}>
              <span>
                <IconButton
                  size="small"
                  disabled={first}
                  onClick={() => onMove(spec.name, -1)}
                  aria-label={t('picker.moveUp')}
                >
                  <ArrowUpwardIcon fontSize="small" />
                </IconButton>
              </span>
            </Tooltip>
            <Tooltip title={t('picker.moveDown')}>
              <span>
                <IconButton
                  size="small"
                  disabled={last}
                  onClick={() => onMove(spec.name, 1)}
                  aria-label={t('picker.moveDown')}
                >
                  <ArrowDownwardIcon fontSize="small" />
                </IconButton>
              </span>
            </Tooltip>
          </>
        ) : null}
        {view?.partial ? <Chip size="small" color="warning" label={t('chart.partial')} /> : null}
        {view?.capped
          ? Object.entries(view.capped).map(([dim, n]) => (
              <Chip
                key={dim}
                size="small"
                color="warning"
                label={t('chart.capped', { dim, count: n })}
              />
            ))
          : null}
      </Stack>

      <Stack direction={{ xs: 'column', md: 'row' }} spacing={2} alignItems="flex-start">
        <CriteriaPanel spec={spec} criteria={criteria} onChange={setCriteria} />

        <Box sx={{ flex: 1, minWidth: 0, width: '100%' }}>
          {showMainOf(criteria) ? (
            <ChartTile
              title={
                view?.filtered
                  ? t('chart.filteredBy', { filter: criteria.groupFilter ?? '' })
                  : t('chart.all')
              }
              subtitle={spec.displayName}
              minimised={minimised[`${spec.name}/`]}
              onToggleMinimised={() => toggleMinimised(`${spec.name}/`)}
            >
              {view ? (
                <MinuteSeriesChart series={mainSeries} showLegend height={tileHeight + 80} />
              ) : (
                <Alert severity="info">{t('chart.waiting')}</Alert>
              )}
            </ChartTile>
          ) : view ? null : (
            <Alert severity="info">{t('chart.waiting')}</Alert>
          )}

          {together.length > 0 ? (
            <Box sx={{ mt: showMainOf(criteria) ? 2 : 0 }}>
              <ChartTile
                title={spec.dims.find((d) => d.name === criteria.group)?.displayName ?? ''}
                subtitle={spec.displayName}
                minimised={minimised[`${spec.name}//together`]}
                onToggleMinimised={() => toggleMinimised(`${spec.name}//together`)}
              >
                <MinuteSeriesChart series={together} showLegend height={tileHeight + 80} />
              </ChartTile>
            </Box>
          ) : null}

          {tiles.length > 0 ? (
            <Box
              sx={{
                mt: showMainOf(criteria) ? 2 : 0,
                display: 'grid',
                gap: 2,
                gridTemplateColumns: { xs: '1fr', lg: `repeat(${columns}, minmax(0, 1fr))` },
              }}
            >
              {tiles.map(({ group, series }) => (
                <ChartTile
                  key={group.value}
                  title={group.value === OTHER_BUCKET ? t('chart.other') : groupName(group)}
                  subtitle={t('chart.groupSummary', {
                    count: group.count,
                    ms: Math.round(group.avgMs),
                  })}
                  minimised={minimised[`${spec.name}/${group.value}`]}
                  onToggleMinimised={() => toggleMinimised(`${spec.name}/${group.value}`)}
                  onMove={(direction) =>
                    move(
                      spec.name,
                      group.value,
                      direction,
                      groups.map((g) => g.value),
                    )
                  }
                >
                  <MinuteSeriesChart
                    series={series}
                    showLegend={showLegend || Boolean(criteria.sub)}
                    height={tileHeight}
                  />
                </ChartTile>
              ))}
            </Box>
          ) : null}
        </Box>
      </Stack>
    </Stack>
  )
})
