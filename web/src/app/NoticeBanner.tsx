import Alert from '@mui/material/Alert'
import Snackbar from '@mui/material/Snackbar'

import type { Notice } from '../store/useMonitorStore'

type Severity = 'success' | 'info' | 'warning' | 'error'

/**
 * A message someone sent to everyone watching. Green and at the top, so it
 * reads as a person talking rather than as another alert — and so it is not
 * missed in the corner of the screen.
 */
export function NoticeBanner({
  notice,
  onDismiss,
  /** A wall display must never wait for someone to click. */
  autoDismissOnly = false,
}: {
  notice: Notice | null
  onDismiss: () => void
  autoDismissOnly?: boolean
}) {
  const sticky = notice?.popup === true && !autoDismissOnly

  return (
    <Snackbar
      open={Boolean(notice)}
      autoHideDuration={sticky ? null : 10_000}
      onClose={onDismiss}
      anchorOrigin={{ vertical: 'top', horizontal: 'center' }}
      sx={{ mt: 1 }}
    >
      <Alert
        severity={severityOf(notice?.level)}
        variant="filled"
        onClose={autoDismissOnly ? undefined : onDismiss}
        sx={{ width: '100%', boxShadow: 6 }}
      >
        {notice?.text ?? ''}
      </Alert>
    </Snackbar>
  )
}

/** A message is good news by default; a sender can still mark it otherwise. */
function severityOf(level: string | undefined): Severity {
  switch (level) {
    case 'error':
      return 'error'
    case 'warn':
    case 'warning':
      return 'warning'
    case 'info':
      return 'info'
    default:
      return 'success'
  }
}
