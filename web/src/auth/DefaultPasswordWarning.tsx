import Alert from '@mui/material/Alert'
import Link from '@mui/material/Link'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { README_URL } from '../app/TabHint'

export function DefaultPasswordWarning() {
  const { t } = useTranslation()
  const [dismissed, setDismissed] = useState(false)
  if (dismissed) return null
  return (
    <Alert severity="warning" square onClose={() => setDismissed(true)}>
      {t('login.defaultPassword')}{' '}
      <Link
        href={`${README_URL}#changing-the-default-password`}
        target="_blank"
        rel="noopener noreferrer"
        color="inherit"
      >
        {t('login.defaultPasswordHow')}
      </Link>
    </Alert>
  )
}
