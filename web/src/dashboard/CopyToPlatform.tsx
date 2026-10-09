import Button from '@mui/material/Button'
import Menu from '@mui/material/Menu'
import MenuItem from '@mui/material/MenuItem'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import type { Dashboard } from '../api/dashboards'
import { copyToPlatform } from './transfer'

export function CopyToPlatformButton({
  dashboard,
  platforms,
  onCopied,
  onError,
}: {
  dashboard: Dashboard
  platforms: string[]
  onCopied: (target: string) => void
  onError: (message: string) => void
}) {
  const { t } = useTranslation()
  const [anchor, setAnchor] = useState<HTMLElement | null>(null)
  const targets = platforms.filter((p) => p !== dashboard.platform)
  if (targets.length === 0) return null

  const copy = async (target: string) => {
    setAnchor(null)
    try {
      await copyToPlatform(dashboard, target)
      onCopied(target)
    } catch (err) {
      onError(err instanceof Error ? err.message : String(err))
    }
  }

  return (
    <>
      <Button size="small" onClick={(e) => setAnchor(e.currentTarget)}>
        {t('dashboard.copyTo')}
      </Button>
      <Menu anchorEl={anchor} open={Boolean(anchor)} onClose={() => setAnchor(null)}>
        {targets.map((p) => (
          <MenuItem key={p} onClick={() => void copy(p)}>
            {p}
          </MenuItem>
        ))}
      </Menu>
    </>
  )
}
