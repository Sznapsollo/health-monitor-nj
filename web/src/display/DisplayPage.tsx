import Alert from '@mui/material/Alert'
import Box from '@mui/material/Box'
import Chip from '@mui/material/Chip'
import Stack from '@mui/material/Stack'
import Typography from '@mui/material/Typography'
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useShallow } from 'zustand/react/shallow'

import type { SignalSpec } from '../api/catalogue'
import { gigabytes } from '../api/server'
import { fetchSession, type DisplayOptions } from '../api/session'
import { MinuteSeriesChart } from '../charts/MinuteSeriesChart'
import {
  groupName,
  showMainOf,
  subStyleOf,
  type SeriesSpec,
  type SeriesStyle,
} from '../charts/options'
import { useGroupTiles } from '../charts/useGroupTiles'
import { loadSaved } from '../criteria/persistence'
import { DashboardPage } from '../dashboard/DashboardPage'
import { useAlertStore } from '../store/useAlertStore'
import { useMonitorStore } from '../store/useMonitorStore'
import { useServerStore } from '../store/useServerStore'
import { useThemeMode } from '../theme/useThemeMode'
import type { SignalView } from '../ws/types'
import { formatElapsed } from '../app/elapsed'
import { GaugesSection } from '../app/GaugesSection'
import { NoticeBanner } from '../app/NoticeBanner'
import { ResourceBadge } from '../app/ResourceBadge'
import { ThemeToggle } from '../app/ThemeToggle'
import type { ThemeMode } from '../theme/tokens'
import { rotationSeconds } from './rotation'

/** A day, after which the page reloads itself as a guard against leaks. */
const SELF_RELOAD_MS = 24 * 60 * 60 * 1000
/**
 * How often the alert and status panels are re-read. Alerts also arrive live
 * over the socket, but what has gone quiet is only noticed by asking, and a
 * screen left alone for weeks must not drift.
 */
const ALERTS_EVERY_MS = 30_000
/** The screen's own theme, kept apart from what Settings imposes on it. */
const SCREEN_THEME_KEY = 'hm.displayTheme'
/** How long the theme switch stays visible after the last touch or mouse move. */
const SWITCH_VISIBLE_MS = 10_000

function readScreenTheme(): ThemeMode {
  try {
    const stored = globalThis.localStorage?.getItem(SCREEN_THEME_KEY)
    return stored === 'light' || stored === 'dark' ? stored : 'system'
  } catch {
    return 'system'
  }
}

function saveScreenTheme(mode: ThemeMode) {
  try {
    globalThis.localStorage?.setItem(SCREEN_THEME_KEY, mode)
  } catch {
    // storage may be blocked; the choice then lasts until the next reload
  }
}

/** How often the screen re-reads its own options and the system figures. */
const OPTIONS_EVERY_MS = 60_000
const SYSTEM_EVERY_MS = 30_000
/** How long without new chart data before "updated" becomes "no new data since". */
const QUIET_AFTER_MS = 2 * 60 * 1000
/** Three missed pings: the connection is gone even if the socket has not noticed. */
const LOST_AFTER_MS = 90 * 1000

/**
 * The wall display: no menus, no dialogs, nothing that waits for a human. It
 * is expected to run for weeks untouched.
 */
export function DisplayPage() {
  const { t } = useTranslation()
  const {
    catalogue,
    platform,
    criteria,
    views,
    visible,
    connection,
    serverTime,
    lastContact,
    notice,
    dismissNotice,
    connect,
    disconnect,
    loadCatalogue,
    dashboards,
    dashboardId,
    pairedDashboard,
  } = useMonitorStore(
    useShallow((s) => ({
      catalogue: s.catalogue,
      platform: s.platform,
      criteria: s.criteria,
      views: s.views,
      visible: s.visible,
      connection: s.connection,
      serverTime: s.serverTime,
      lastContact: s.lastContact,
      notice: s.notice,
      dismissNotice: s.dismissNotice,
      connect: s.connect,
      disconnect: s.disconnect,
      loadCatalogue: s.loadCatalogue,
      dashboards: s.dashboards,
      dashboardId: s.dashboardId,
      pairedDashboard: s.pairedDashboard,
    })),
  )
  const loadAlerts = useAlertStore((s) => s.load)
  const loadSilences = useAlertStore((s) => s.loadSilences)
  const loadServer = useServerStore((s) => s.load)
  const disks = useServerStore((s) => s.resources?.disks)
  const { setMode } = useThemeMode()
  const [options, setOptions] = useState<DisplayOptions | null>(null)
  const [now, setNow] = useState(() => Date.now())

  const params = useMemo(() => new URLSearchParams(globalThis.location?.search ?? ''), [])
  const rotateSeconds = rotationSeconds(params.get('rotate'))
  const requestedView = params.get('view')
  const all = useMemo(() => loadSaved(), [])
  // `?view=a,b` picks which sets this screen shows; without it, all of them.
  const saved = useMemo(() => {
    if (!requestedView) return all
    const wanted = requestedView.split(',').map((n) => n.trim())
    const picked = all.filter((s) => wanted.includes(s.name))
    return picked.length > 0 ? picked : all
  }, [all, requestedView])
  const [rotation, setRotation] = useState(0)

  // What this screen shows, most deliberate first: the dashboard its token was
  // paired with, then a saved set it was linked to, then whichever dashboard
  // the platform opens on. Only with none of those does it fall back to the
  // charts this browser last looked at.
  const board = useMemo(() => {
    const paired = dashboards.find((d) => d.id === pairedDashboard)
    if (paired) return paired
    if (requestedView) return undefined
    return dashboards.find((d) => d.id === dashboardId)
  }, [dashboards, dashboardId, pairedDashboard, requestedView])

  useEffect(() => {
    void loadCatalogue()
    connect()
    return () => disconnect()
  }, [connect, disconnect, loadCatalogue])

  // The alert and status panels read from their own store, which nothing else
  // on this page fills: without this a dashboard's alerts and status panels
  // sit empty on the wall.
  useEffect(() => {
    if (!platform) return
    const read = () => {
      void loadAlerts(platform)
      void loadSilences(platform)
    }
    read()
    const handle = globalThis.setInterval(read, ALERTS_EVERY_MS)
    return () => globalThis.clearInterval(handle)
  }, [platform, loadAlerts, loadSilences])

  // Options are changed in Settings while the screen runs; re-reading them
  // means nobody has to walk to the wall to reload it.
  useEffect(() => {
    const read = () => {
      fetchSession()
        .then((s) => setOptions(s.display ?? {}))
        .catch(() => undefined)
    }
    read()
    const handle = globalThis.setInterval(read, OPTIONS_EVERY_MS)
    return () => globalThis.clearInterval(handle)
  }, [])

  const screenChooses = options?.theme === 'screen'
  useEffect(() => {
    if (!options) return
    if (options.theme === 'screen') setMode(readScreenTheme())
    else setMode(options.theme ?? 'system')
  }, [options, setMode])

  // The switch shows only while someone is at the screen.
  const [touched, setTouched] = useState(0)
  useEffect(() => {
    if (!screenChooses) return
    const wake = () => setTouched(Date.now())
    globalThis.addEventListener('pointermove', wake)
    globalThis.addEventListener('pointerdown', wake)
    return () => {
      globalThis.removeEventListener('pointermove', wake)
      globalThis.removeEventListener('pointerdown', wake)
    }
  }, [screenChooses])
  const switchVisible = screenChooses && Date.now() - touched < SWITCH_VISIBLE_MS
  useEffect(() => {
    if (!switchVisible) return
    const handle = globalThis.setTimeout(() => setTouched(0), SWITCH_VISIBLE_MS)
    return () => globalThis.clearTimeout(handle)
  }, [switchVisible, touched])

  // Read even when the figures are not shown: a full disk is always shown.
  useEffect(() => {
    void loadServer()
    const handle = globalThis.setInterval(() => void loadServer(), SYSTEM_EVERY_MS)
    return () => globalThis.clearInterval(handle)
  }, [loadServer])

  // A ticking clock, so "stale since" is honest without any data arriving.
  useEffect(() => {
    const handle = globalThis.setInterval(() => setNow(Date.now()), 10_000)
    return () => globalThis.clearInterval(handle)
  }, [])

  // A nightly reload: cheap insurance against a browser that has been open
  // for weeks.
  useEffect(() => {
    const handle = globalThis.setTimeout(() => globalThis.location.reload(), SELF_RELOAD_MS)
    return () => globalThis.clearTimeout(handle)
  }, [])

  // A screen pointed at one named set shows it and stays there.
  useEffect(() => {
    if (board || !requestedView || saved.length === 0) return
    const first = saved[0]
    // A set names its charts as well as their filters, so the screen shows
    // exactly the group it was pointed at.
    if (first) useMonitorStore.getState().applyCriteria(first.criteria, first.name, first.visible)
  }, [board, requestedView, saved])

  // Rotation through saved filter sets is what makes one screen useful to a
  // whole team.
  useEffect(() => {
    if (board || rotateSeconds <= 0 || saved.length < 2) return
    const handle = globalThis.setInterval(
      () => setRotation((r) => (r + 1) % saved.length),
      rotateSeconds * 1000,
    )
    return () => globalThis.clearInterval(handle)
  }, [board, rotateSeconds, saved.length])

  useEffect(() => {
    if (board || rotateSeconds <= 0 || saved.length < 2) return
    const set = saved[rotation]
    if (set) useMonitorStore.getState().applyCriteria(set.criteria, undefined, set.visible)
  }, [board, rotation, rotateSeconds, saved])

  const allSpecs = catalogue?.platforms.find((p) => p.name === platform)?.signals ?? []
  // Only the charts the set asks for: the others are not subscribed to, so
  // drawing them would draw nothing.
  const signals: SignalSpec[] = allSpecs.filter(
    (s) => s.kind === 'timeseries' && visible.includes(s.name),
  )

  const silentFor = lastContact ? now - lastContact : 0
  const lost = connection !== 'open' || silentFor > LOST_AFTER_MS
  const quiet = serverTime !== null && now - new Date(serverTime).getTime() > QUIET_AFTER_MS
  const time = (at: string | null) => (at ? new Date(at).toLocaleTimeString() : '—')

  return (
    <Box sx={{ p: 2, minHeight: '100vh' }}>
      <Stack direction="row" alignItems="center" spacing={2} sx={{ mb: 2 }}>
        <Typography variant="h5" component="h1" sx={{ flexGrow: 1 }}>
          {t('app.name')}
        </Typography>
        {board ? <Chip size="small" label={board.name} /> : null}
        {!board && saved.length > 0 && (rotateSeconds > 0 || requestedView) ? (
          <Chip size="small" label={saved[rotateSeconds > 0 ? rotation : 0]?.name ?? ''} />
        ) : null}
        {screenChooses ? (
          <Box
            sx={{
              opacity: switchVisible ? 1 : 0,
              transition: 'opacity 0.4s',
              pointerEvents: switchVisible ? 'auto' : 'none',
            }}
          >
            <ThemeToggle onPick={saveScreenTheme} />
          </Box>
        ) : null}
        {options?.showSystem ? <ResourceBadge /> : null}
        {lost ? (
          <Chip
            color="warning"
            label={t('display.lost', { age: formatElapsed(silentFor / 1000) })}
          />
        ) : options?.showLive ? (
          <Chip
            color="success"
            variant="outlined"
            label={t(quiet ? 'display.quiet' : 'display.live', { time: time(serverTime) })}
          />
        ) : null}
      </Stack>

      {(disks ?? [])
        .filter((d) => d.low)
        .map((d) => (
          <Alert key={d.folders.join()} severity="error" sx={{ mb: 2 }}>
            {t('display.diskLow', {
              free: gigabytes(d.freeBytes),
              total: gigabytes(d.totalBytes),
            })}
          </Alert>
        ))}

      {board ? (
        // A dashboard is an arrangement someone decided on; nothing is
        // appended to it, and there are no tabs here to open, so the panels
        // carry no "open the tab" buttons.
        <DashboardPage dashboard={board} platform={platform} specs={allSpecs} />
      ) : (
        <Stack spacing={3}>
          {signals.map((spec) => (
            <DisplaySignal
              key={spec.name}
              spec={spec}
              view={views[spec.name]}
              subStyle={subStyleOf(criteria[spec.name] ?? {})}
              showMain={showMainOf(criteria[spec.name] ?? {})}
            />
          ))}

          <GaugesSection platform={platform} specs={allSpecs} alwaysPoll />
        </Stack>
      )}

      {/* A message reaches the screen, but never waits for a human to close it. */}
      <NoticeBanner notice={notice} onDismiss={dismissNotice} autoDismissOnly />
    </Box>
  )
}

function DisplaySignal({
  spec,
  view,
  subStyle,
  showMain,
}: {
  spec: SignalSpec
  view: SignalView | undefined
  subStyle: SeriesStyle
  showMain: boolean
}) {
  const { t } = useTranslation()
  const main: SeriesSpec[] = useMemo(() => {
    const total = view?.total ?? []
    return [
      { name: t('chart.count'), points: total, value: 'count', style: 'column' },
      { name: t('chart.latency'), points: total, value: 'avgMs', style: 'line', secondary: true },
    ]
  }, [view?.total, t])
  const groups = view?.groups
  const shown = useMemo(() => (groups ?? []).slice(0, 6), [groups])
  const tiles = useGroupTiles(shown, subStyle, t('chart.rest'), spec.colors)
  return (
    <Box>
      <Typography variant="h6" component="h2" gutterBottom>
        {spec.displayName}
      </Typography>
      {showMain ? <MinuteSeriesChart series={main} showLegend height={280} /> : null}
      {tiles.length > 0 ? (
        <Box
          sx={{
            mt: showMain ? 2 : 0,
            display: 'grid',
            gap: 2,
            gridTemplateColumns: { xs: '1fr', md: '1fr 1fr', xl: '1fr 1fr 1fr' },
          }}
        >
          {tiles.map(({ group, series }) => (
            <Box key={group.value}>
              <Typography variant="body2" noWrap title={group.value}>
                {groupName(group)}
              </Typography>
              <MinuteSeriesChart series={series} height={160} />
            </Box>
          ))}
        </Box>
      ) : null}
    </Box>
  )
}
