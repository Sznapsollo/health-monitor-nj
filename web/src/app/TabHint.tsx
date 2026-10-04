import Link from '@mui/material/Link'
import Typography from '@mui/material/Typography'
import { useTranslation } from 'react-i18next'

export const README_URL = 'https://github.com/Sznapsollo/health-monitor-nj/blob/main/README.md'

const README_SECTIONS = {
  dashboard: ['dashboard', 'Dashboard'],
  charts: ['charts', 'Charts'],
  alerts: ['alerts', 'Alerts'],
  status: ['status', 'Status'],
  search: ['search', 'Search'],
  activity: ['platform-users', 'Platform users'],
  rules: ['alert-rules', 'Alert rules'],
  signals: ['signals', 'Signals'],
  storage: ['storage', 'Storage'],
  info: ['info', 'Info'],
  settings: ['settings', 'Settings'],
  health: ['hm-errors', 'HM errors'],
} as const

export type HintedTab = keyof typeof README_SECTIONS

/** What a tab is for, in a line, with the README section that says more. */
export function TabHint({ tab }: { tab: HintedTab }) {
  const { t } = useTranslation()
  const [anchor, chapter] = README_SECTIONS[tab]
  return (
    <Typography variant="body2" color="text.secondary">
      {t(`hints.${tab}`)}{' '}
      <Link href={`${README_URL}#${anchor}`} target="_blank" rel="noopener noreferrer">
        {t('hints.more', { chapter })}
      </Link>
    </Typography>
  )
}
