import Box from '@mui/material/Box'
import LinearProgress from '@mui/material/LinearProgress'
import Stack from '@mui/material/Stack'
import Typography from '@mui/material/Typography'

import { tokens } from '../theme/tokens'
import { useThemeMode } from '../theme/useThemeMode'
import { type GaugeSort, sortPoints } from './gaugeSort'

export interface GaugePoint {
  label: string
  value: number
  warn?: boolean
  unit?: string
}

/**
 * The latest value per label as horizontal bars — the old queue-load and
 * VPN-users charts. Bars rather than a canvas chart: there is no time axis
 * here, only "how much, right now".
 */
export function BarGauge({ points, sort = 'label' }: { points: GaugePoint[]; sort?: GaugeSort }) {
  const { resolved } = useThemeMode()
  const palette = tokens[resolved]
  const max = Math.max(1, ...points.map((p) => p.value))

  return (
    <Stack spacing={1}>
      {sortPoints(points, sort).map((p) => {
        const reading = `${p.value.toLocaleString()}${p.unit ? ' ' + p.unit : ''}`
        return (
          <Box key={p.label}>
            <Stack direction="row" justifyContent="space-between">
              <Typography variant="caption" noWrap title={p.label}>
                {p.label}
              </Typography>
              <Typography
                variant="caption"
                sx={{ color: p.warn ? palette.level.error : undefined }}
              >
                {reading}
              </Typography>
            </Stack>
            <LinearProgress
              variant="determinate"
              value={Math.min(100, (p.value / max) * 100)}
              sx={{
                height: 8,
                borderRadius: 1,
                '& .MuiLinearProgress-bar': {
                  backgroundColor: p.warn ? palette.level.error : palette.primary,
                },
              }}
            />
          </Box>
        )
      })}
    </Stack>
  )
}
