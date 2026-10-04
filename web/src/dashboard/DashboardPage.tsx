import Box from '@mui/material/Box'
import Stack from '@mui/material/Stack'
import { useEffect, useState } from 'react'

import type { SignalSpec } from '../api/catalogue'
import { panelId, type Dashboard } from '../api/dashboards'
import { fetchGauges, type GaugeView } from '../api/session'
import { DashboardPanel, type PanelTab } from './DashboardPanel'
import { columnTemplate } from './layout'
import { everyVisible } from '../app/everyVisible'

interface Props {
  dashboard: Dashboard
  platform: string
  specs: SignalSpec[]
  /** Each panel's header offers to open the tab it is a slice of. */
  onOpen?: (tab: PanelTab) => void
  filterable?: boolean
}

/**
 * A dashboard is columns side by side, panels stacked inside each. The widths
 * are relative, so `2` and `1` is two thirds and one third; on a narrow screen
 * the columns stack instead.
 */
export function DashboardPage({ dashboard, platform, specs, onOpen, filterable }: Props) {
  const [gauges, setGauges] = useState<GaugeView[]>([])

  // Gauges carry no history, so they refresh on their own rather than
  // arriving over the live stream.
  const hasGauges = dashboard.rows.some((row) =>
    row.columns.some((column) => column.panels.some((panel) => panel.type === 'gauge')),
  )
  useEffect(() => {
    if (!platform || !hasGauges) return
    let cancelled = false
    const load = () => {
      fetchGauges(platform)
        .then((list) => !cancelled && setGauges(list))
        .catch(() => !cancelled && setGauges([]))
    }
    load()
    const stop = everyVisible(load, 10_000)
    return () => {
      cancelled = true
      stop()
    }
  }, [platform, hasGauges])

  return (
    <Stack spacing={2}>
      {dashboard.rows.map((row, r) => (
        <Box
          key={r}
          sx={{
            display: 'grid',
            gap: 2,
            gridTemplateColumns: { xs: 'minmax(0, 1fr)', md: columnTemplate(row.columns) },
            alignItems: 'start',
          }}
        >
          {row.columns.map((column, i) => (
            <Stack key={i} spacing={2}>
              {column.panels.map((panel, j) => (
                <DashboardPanel
                  key={panelId(dashboard, r, i, j)}
                  viewId={panelId(dashboard, r, i, j)}
                  panel={panel}
                  platform={platform}
                  specs={specs}
                  gauges={gauges}
                  onOpen={onOpen}
                  filterable={filterable}
                />
              ))}
            </Stack>
          ))}
        </Box>
      ))}
    </Stack>
  )
}
