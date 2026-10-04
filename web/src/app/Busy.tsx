import CircularProgress from '@mui/material/CircularProgress'
import LinearProgress from '@mui/material/LinearProgress'
import Stack from '@mui/material/Stack'
import Typography from '@mui/material/Typography'
import { useTranslation } from 'react-i18next'

/**
 * Says that data is on its way: a spinner with a label where there is nothing
 * to show yet, or a thin bar above content that stays while it refreshes.
 */
export function Busy({ label, bar = false }: { label?: string; bar?: boolean }) {
  const { t } = useTranslation()
  const text = label ?? t('common.loading')
  if (bar) return <LinearProgress aria-label={text} sx={{ height: 2 }} />
  return (
    <Stack direction="row" spacing={1} alignItems="center" role="status" aria-live="polite">
      <CircularProgress size={16} aria-hidden />
      <Typography variant="body2" color="text.secondary">
        {text}
      </Typography>
    </Stack>
  )
}
