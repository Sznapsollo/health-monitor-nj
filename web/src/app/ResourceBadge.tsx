import Chip from '@mui/material/Chip'
import Tooltip from '@mui/material/Tooltip'
import { useTranslation } from 'react-i18next'

import { gigabytes, megabytes, strained } from '../api/server'
import { useServerStore } from '../store/useServerStore'

/** The monitor's own memory and CPU, always in view; details on hover. */
export function ResourceBadge() {
  const { t } = useTranslation()
  const r = useServerStore((s) => s.resources)
  if (!r) return null
  const loud = strained(r)
  const lowDisk = (r.disks ?? []).find((d) => d.low)
  const memory = r.memLimitBytes
    ? t('server.memoryOfLimit', { used: megabytes(r.rssBytes), limit: megabytes(r.memLimitBytes) })
    : megabytes(r.rssBytes)
  const details = [
    `${t('server.memory')}: ${memory}`,
    `${t('server.heap')}: ${megabytes(r.heapBytes)}`,
    `${t('server.cpu1m')}: ${r.cpuPercent1m} % (${t('server.cores', { count: r.cores })})`,
    ...(r.dbBytes ? [`${t('server.databaseSize')}: ${megabytes(r.dbBytes)}`] : []),
    ...(r.packetsLastMinute !== undefined
      ? [
          `${t('server.packets')}: ${t('server.packetsCount', { count: r.packetsLastMinute.toLocaleString() })}`,
        ]
      : []),
    ...(r.disks ?? []).map(
      (d) =>
        `${t('server.disk')}: ${t('server.diskFree', { free: gigabytes(d.freeBytes) })} / ${gigabytes(d.totalBytes)}`,
    ),
    ...(r.logsLastMinute !== undefined
      ? [
          `${t('server.logs')}: ${t('server.logsCount', { count: r.logsLastMinute.toLocaleString() })}`,
        ]
      : []),
  ].join(' · ')
  const packets =
    r.packetsLastMinute !== undefined
      ? ` · ${t('server.packetsBadge', { count: r.packetsLastMinute.toLocaleString() })}`
      : ''
  const logs =
    r.logsLastMinute !== undefined
      ? ` · ${t('server.logsBadge', { count: r.logsLastMinute.toLocaleString() })}`
      : ''
  return (
    <Tooltip title={details}>
      <Chip
        size="small"
        variant="outlined"
        sx={{ fontVariantNumeric: 'tabular-nums' }}
        color={lowDisk ? 'error' : loud.memory || loud.cpu ? 'warning' : 'default'}
        label={
          (r.dbBytes
            ? t('server.badgeWithDb', {
                memory: megabytes(r.rssBytes),
                cpu: r.cpuPercent1m,
                db: megabytes(r.dbBytes),
              })
            : t('server.badge', { memory: megabytes(r.rssBytes), cpu: r.cpuPercent1m })) +
          packets +
          logs +
          (lowDisk ? ` · ${t('server.diskBadge', { free: gigabytes(lowDisk.freeBytes) })}` : '')
        }
      />
    </Tooltip>
  )
}
