import ChevronLeftIcon from '@mui/icons-material/ChevronLeft'
import ChevronRightIcon from '@mui/icons-material/ChevronRight'
import ExpandLessIcon from '@mui/icons-material/ExpandLess'
import ExpandMoreIcon from '@mui/icons-material/ExpandMore'
import Card from '@mui/material/Card'
import CardContent from '@mui/material/CardContent'
import IconButton from '@mui/material/IconButton'
import Stack from '@mui/material/Stack'
import Tooltip from '@mui/material/Tooltip'
import Typography from '@mui/material/Typography'
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

interface Props {
  title: string
  subtitle?: string
  minimised?: boolean
  onToggleMinimised?: () => void
  onMove?: (direction: 1 | -1) => void
  actions?: ReactNode
  children: ReactNode
}

/** A chart with the toolbar the old UI had: minimise and move. */
export function ChartTile({
  title,
  subtitle,
  minimised,
  onToggleMinimised,
  onMove,
  actions,
  children,
}: Props) {
  const { t } = useTranslation()

  return (
    <Card variant="outlined" sx={{ height: '100%' }}>
      <CardContent sx={{ pb: 1 }}>
        <Stack direction="row" alignItems="center" justifyContent="space-between" spacing={1}>
          <Stack sx={{ minWidth: 0 }}>
            <Typography variant="subtitle2" noWrap title={title}>
              {title}
            </Typography>
            {subtitle ? (
              <Typography variant="caption" color="text.secondary" noWrap>
                {subtitle}
              </Typography>
            ) : null}
          </Stack>

          <Stack direction="row" alignItems="center" flexShrink={0}>
            {actions}
            {onMove ? (
              <>
                <Tooltip title={t('tile.moveLeft')}>
                  <IconButton
                    size="small"
                    onClick={() => onMove(-1)}
                    aria-label={t('tile.moveLeft')}
                  >
                    <ChevronLeftIcon fontSize="small" />
                  </IconButton>
                </Tooltip>
                <Tooltip title={t('tile.moveRight')}>
                  <IconButton
                    size="small"
                    onClick={() => onMove(1)}
                    aria-label={t('tile.moveRight')}
                  >
                    <ChevronRightIcon fontSize="small" />
                  </IconButton>
                </Tooltip>
              </>
            ) : null}
            {onToggleMinimised ? (
              <Tooltip title={minimised ? t('tile.expand') : t('tile.minimise')}>
                <IconButton
                  size="small"
                  onClick={onToggleMinimised}
                  aria-label={minimised ? t('tile.expand') : t('tile.minimise')}
                >
                  {minimised ? (
                    <ExpandMoreIcon fontSize="small" />
                  ) : (
                    <ExpandLessIcon fontSize="small" />
                  )}
                </IconButton>
              </Tooltip>
            ) : null}
          </Stack>
        </Stack>

        {minimised ? null : children}
      </CardContent>
    </Card>
  )
}
