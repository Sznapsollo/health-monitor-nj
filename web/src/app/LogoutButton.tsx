import LogoutIcon from '@mui/icons-material/Logout'
import Button from '@mui/material/Button'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { fetchSession, logout, type SessionInfo } from '../api/session'

/** Who is logged in, and the way out; absent when the monitor has no password. */
export function LogoutButton() {
  const { t } = useTranslation()
  const [session, setSession] = useState<SessionInfo | null>(null)

  useEffect(() => {
    const controller = new AbortController()
    fetchSession(controller.signal)
      .then(setSession)
      .catch(() => setSession(null))
    return () => controller.abort()
  }, [])

  if (!session?.required || !session.authenticated || session.kind === 'display') return null
  return (
    <Button
      size="small"
      color="inherit"
      startIcon={<LogoutIcon fontSize="small" />}
      title={t('settings.loggedInAs', { name: session.name ?? '' })}
      onClick={() => void logout().then(() => globalThis.location.reload())}
    >
      {t('settings.logout')}
    </Button>
  )
}
