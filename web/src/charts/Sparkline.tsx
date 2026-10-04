import Box from '@mui/material/Box'

import { tokens } from '../theme/tokens'
import { useThemeMode } from '../theme/useThemeMode'

/** A trend line with no axes: how a value moved, not its exact readings. */
export function Sparkline({ values, label }: { values: number[]; label: string }) {
  const { resolved } = useThemeMode()
  if (values.length < 2) return null
  const max = Math.max(...values, 1)
  const points = values
    .map((v, i) => `${(i / (values.length - 1)) * 100},${40 - (v / max) * 38}`)
    .join(' ')
  return (
    <Box
      component="svg"
      viewBox="0 0 100 40"
      preserveAspectRatio="none"
      role="img"
      aria-label={label}
      sx={{ width: '100%', height: 48, display: 'block' }}
    >
      <polyline
        points={points}
        fill="none"
        stroke={tokens[resolved].primary}
        strokeWidth={1.5}
        vectorEffect="non-scaling-stroke"
      />
    </Box>
  )
}
