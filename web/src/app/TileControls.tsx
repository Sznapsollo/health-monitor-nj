import LegendToggleIcon from '@mui/icons-material/LegendToggle'
import ZoomInIcon from '@mui/icons-material/ZoomIn'
import ZoomOutIcon from '@mui/icons-material/ZoomOut'
import IconButton from '@mui/material/IconButton'
import MenuItem from '@mui/material/MenuItem'
import Select from '@mui/material/Select'
import Stack from '@mui/material/Stack'
import Tooltip from '@mui/material/Tooltip'
import { useTranslation } from 'react-i18next'

import { useUiStore } from '../store/useUiStore'

/** The controls that apply to every tile: legend, zoom and tile width. */
export function TileControls() {
  const { t } = useTranslation()
  const { showLegend, columns, toggleLegend, zoom, setColumns } = useUiStore()

  return (
    <Stack direction="row" spacing={0.5} alignItems="center">
      <Tooltip title={t('tile.legend')}>
        <IconButton
          size="small"
          color={showLegend ? 'primary' : 'default'}
          onClick={toggleLegend}
          aria-label={t('tile.legend')}
        >
          <LegendToggleIcon fontSize="small" />
        </IconButton>
      </Tooltip>
      <Tooltip title={t('tile.zoomOut')}>
        <IconButton size="small" onClick={() => zoom(-1)} aria-label={t('tile.zoomOut')}>
          <ZoomOutIcon fontSize="small" />
        </IconButton>
      </Tooltip>
      <Tooltip title={t('tile.zoomIn')}>
        <IconButton size="small" onClick={() => zoom(1)} aria-label={t('tile.zoomIn')}>
          <ZoomInIcon fontSize="small" />
        </IconButton>
      </Tooltip>
      <Select
        size="small"
        value={columns}
        onChange={(e) => setColumns(Number(e.target.value))}
        aria-label={t('tile.columns')}
        sx={{ minWidth: 72 }}
      >
        {[1, 2, 3, 4].map((n) => (
          <MenuItem key={n} value={n}>
            {t('tile.columnCount', { count: n })}
          </MenuItem>
        ))}
      </Select>
    </Stack>
  )
}
