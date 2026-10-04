import Tooltip from '@mui/material/Tooltip'
import Typography from '@mui/material/Typography'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { fetchVersion, type BuildVersion } from '../api/server'

/** Which build is running, beside the title; details on hover. */
export function VersionTag() {
  const { t } = useTranslation()
  const [build, setBuild] = useState<BuildVersion | null>(null)

  useEffect(() => {
    const controller = new AbortController()
    fetchVersion(controller.signal)
      .then(setBuild)
      .catch(() => setBuild(null))
    return () => controller.abort()
  }, [])

  if (!build) return null
  const label =
    build.commit && build.commit !== 'none'
      ? `health-monitor-nj · v${build.version} · ${build.commit}`
      : `health-monitor-nj · v${build.version}`
  return (
    <Tooltip
      title={t('app.versionDetail', {
        version: build.version,
        commit: build.commit,
        go: build.goVersion,
      })}
    >
      <Typography variant="caption" color="text.secondary" sx={{ whiteSpace: 'nowrap' }}>
        {label}
      </Typography>
    </Tooltip>
  )
}
