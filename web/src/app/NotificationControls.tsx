import NotificationsActiveIcon from '@mui/icons-material/NotificationsActive'
import NotificationsOffIcon from '@mui/icons-material/NotificationsOff'
import VolumeOffIcon from '@mui/icons-material/VolumeOff'
import VolumeUpIcon from '@mui/icons-material/VolumeUp'
import IconButton from '@mui/material/IconButton'
import Stack from '@mui/material/Stack'
import Tooltip from '@mui/material/Tooltip'
import { useTranslation } from 'react-i18next'

import { requestPermission } from '../alerts/notifications'
import { useAlertStore } from '../store/useAlertStore'

/** Sound and browser notifications, per viewer, as the old settings dialog. */
export function NotificationControls() {
  const { t } = useTranslation()
  const { notifications, setNotifications } = useAlertStore()

  const toggleBrowser = async () => {
    if (notifications.browser) {
      setNotifications({ ...notifications, browser: false })
      return
    }
    // Permission is only asked for when the viewer switches it on, never on
    // load — a wall display must never be blocked by a prompt.
    const granted = await requestPermission()
    setNotifications({ ...notifications, browser: granted })
  }

  return (
    <Stack direction="row">
      <Tooltip title={notifications.sound ? t('notify.soundOn') : t('notify.soundOff')}>
        <IconButton
          size="small"
          aria-label={notifications.sound ? t('notify.soundOn') : t('notify.soundOff')}
          color={notifications.sound ? 'primary' : 'default'}
          onClick={() => setNotifications({ ...notifications, sound: !notifications.sound })}
        >
          {notifications.sound ? (
            <VolumeUpIcon fontSize="small" />
          ) : (
            <VolumeOffIcon fontSize="small" />
          )}
        </IconButton>
      </Tooltip>
      <Tooltip title={notifications.browser ? t('notify.browserOn') : t('notify.browserOff')}>
        <IconButton
          size="small"
          aria-label={notifications.browser ? t('notify.browserOn') : t('notify.browserOff')}
          color={notifications.browser ? 'primary' : 'default'}
          onClick={() => void toggleBrowser()}
        >
          {notifications.browser ? (
            <NotificationsActiveIcon fontSize="small" />
          ) : (
            <NotificationsOffIcon fontSize="small" />
          )}
        </IconButton>
      </Tooltip>
    </Stack>
  )
}
