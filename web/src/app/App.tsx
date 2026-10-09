import Alert from '@mui/material/Alert'
import AppBar from '@mui/material/AppBar'
import Badge from '@mui/material/Badge'
import Box from '@mui/material/Box'
import Container from '@mui/material/Container'
import Button from '@mui/material/Button'
import MenuItem from '@mui/material/MenuItem'
import Select from '@mui/material/Select'
import Stack from '@mui/material/Stack'
import Tab from '@mui/material/Tab'
import Tabs from '@mui/material/Tabs'
import Toolbar from '@mui/material/Toolbar'
import Typography from '@mui/material/Typography'
import { lazy, Suspense, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useShallow } from 'zustand/react/shallow'

import { AlertsPanel } from '../alerts/AlertsPanel'
import { StatusTable } from '../alerts/StatusTable'
import { useAlertStore } from '../store/useAlertStore'
import { useHealthStore } from '../store/useHealthStore'
import { useServerStore } from '../store/useServerStore'
import { useMonitorStore } from '../store/useMonitorStore'
import { deleteDashboard, saveDashboard, slugOf, type Dashboard } from '../api/dashboards'
import {
  download,
  exportAllFileName,
  exportFileName,
  exportText,
  parseImport,
} from '../dashboard/transfer'
import { CopyToPlatformButton } from '../dashboard/CopyToPlatform'
import { DashboardPage } from '../dashboard/DashboardPage'
import { ChartsPage } from './ChartsPage'
import { LiveConnectionBadge } from './ConnectionBadge'
import { NoticeBanner } from './NoticeBanner'
import { NotificationControls } from './NotificationControls'
import { LogoutButton } from './LogoutButton'
import { ResourceBadge } from './ResourceBadge'
import { ServerStats } from './ServerStats'
import { ViewersTable } from './ViewersTable'
import { VisitsTable } from './VisitsTable'
import { ServerStatus } from './ServerStatus'
import { ThemeToggle } from './ThemeToggle'
import { VersionTag } from './VersionTag'
import { Busy } from './Busy'
import { everyVisible } from './everyVisible'
import { TabHint } from './TabHint'

const ActivityPanel = lazy(() =>
  import('../search/ActivityPanel').then((m) => ({ default: m.ActivityPanel })),
)
const StoragePanel = lazy(() =>
  import('../storage/StoragePanel').then((m) => ({ default: m.StoragePanel })),
)
const InfoPanel = lazy(() => import('../info/InfoPanel').then((m) => ({ default: m.InfoPanel })))
const SearchPanel = lazy(() =>
  import('../search/SearchPanel').then((m) => ({ default: m.SearchPanel })),
)
const RulesPanel = lazy(() =>
  import('../settings/RulesPanel').then((m) => ({ default: m.RulesPanel })),
)
const SettingsPanel = lazy(() =>
  import('../settings/SettingsPanel').then((m) => ({ default: m.SettingsPanel })),
)
const DashboardEditor = lazy(() =>
  import('../dashboard/DashboardEditor').then((m) => ({ default: m.DashboardEditor })),
)
const SignalsPage = lazy(() =>
  import('../designer/SignalsPage').then((m) => ({ default: m.SignalsPage })),
)
const HealthPanel = lazy(() => import('./HealthPanel').then((m) => ({ default: m.HealthPanel })))

type TabName =
  | 'dashboard'
  | 'charts'
  | 'alerts'
  | 'status'
  | 'search'
  | 'activity'
  | 'storage'
  | 'info'
  | 'rules'
  | 'signals'
  | 'settings'
  | 'health'

/** How often the servers' up/down state and last-seen times are re-read. */
const STATUS_EVERY_MS = 10_000

export function App() {
  const { t } = useTranslation()
  const {
    catalogue,
    catalogueError,
    platform,
    platforms,
    notice,
    dashboards,
    dashboardId,
    openDashboard,
    reloadDashboards,
    connect,
    disconnect,
    loadCatalogue,
    setPlatform,
    dismissNotice,
  } = useMonitorStore(
    useShallow((s) => ({
      catalogue: s.catalogue,
      catalogueError: s.catalogueError,
      platform: s.platform,
      platforms: s.platforms,
      notice: s.notice,
      dashboards: s.dashboards,
      dashboardId: s.dashboardId,
      openDashboard: s.openDashboard,
      reloadDashboards: s.reloadDashboards,
      connect: s.connect,
      disconnect: s.disconnect,
      loadCatalogue: s.loadCatalogue,
      setPlatform: s.setPlatform,
      dismissNotice: s.dismissNotice,
    })),
  )
  const loud = useAlertStore(
    (s) =>
      s.alerts.filter((a) => !a.silenced && (a.level === 'ERROR' || a.level === 'WARN')).length,
  )
  const offline = useAlertStore((s) => s.offline)
  const load = useAlertStore((s) => s.load)
  const loadSilences = useAlertStore((s) => s.loadSilences)
  const unknownIssues = useHealthStore((s) => s.unknown)
  const loadIssues = useHealthStore((s) => s.load)
  const loadServer = useServerStore((s) => s.load)
  const [tab, setTab] = useState<TabName>('charts')
  const [draft, setDraft] = useState<Dashboard | null>(null)
  const [dashboardError, setDashboardError] = useState<string | null>(null)
  const [dashboardInfo, setDashboardInfo] = useState<string | null>(null)
  const [copied, setCopied] = useState<{ name: string; from: string; to: string } | null>(null)
  const importInput = useRef<HTMLInputElement>(null)

  // An arrangement, when the platform defines one, is what most people want
  // to open on; the Charts tab stays for building a view by hand.
  const [followedDashboard, setFollowedDashboard] = useState(false)
  useEffect(() => {
    if (followedDashboard || dashboards.length === 0) return
    setFollowedDashboard(true)
    setTab('dashboard')
  }, [dashboards, followedDashboard])

  useEffect(() => {
    void loadCatalogue()
    connect()
    return () => disconnect()
  }, [connect, disconnect, loadCatalogue])

  useEffect(() => {
    void loadIssues()
    return everyVisible(() => void loadIssues(), 30_000)
  }, [loadIssues])

  // The Status tab polls the server itself, with history; one poll is enough.
  const statusOpen = tab === 'status'
  useEffect(() => {
    if (statusOpen) return
    void loadServer()
    return everyVisible(() => void loadServer(), 10_000)
  }, [loadServer, statusOpen])

  useEffect(() => {
    if (!platform) return
    void load(platform)
    void loadSilences(platform)
  }, [platform, load, loadSilences])

  // Heartbeats change who is up and when each was last seen all the time;
  // alerts arrive live, but these only by asking.
  const loadStatus = useAlertStore((s) => s.loadStatus)
  useEffect(() => {
    if (!platform) return
    return everyVisible(() => void loadStatus(platform), STATUS_EVERY_MS)
  }, [platform, loadStatus])

  const allSignals = catalogue?.platforms.find((p) => p.name === platform)?.signals ?? []
  const signals = allSignals.filter((s) => s.kind === 'timeseries')
  const current = dashboards.find((d) => d.id === dashboardId) ?? dashboards[0]

  const newDashboard = (from?: Dashboard) => {
    const name = from ? t('dashboard.copyOf', { name: from.name }) : t('dashboard.newName')
    const id = slugOf(
      name,
      dashboards.map((d) => d.id),
    )
    setDraft({
      id,
      platform,
      name,
      rows: from ? structuredClone(from.rows) : [{ columns: [{ width: 1, panels: [] }] }],
    })
  }
  // One dashboard opens in the editor to be looked over; a set is saved as
  // it comes, each under a new id when its own is taken.
  const importDashboards = async (file: File) => {
    setDashboardError(null)
    setDashboardInfo(null)
    try {
      const found = parseImport(await file.text())
      const taken = dashboards.map((d) => d.id)
      const first = found[0]
      if (found.length === 1 && first) {
        setDraft({ id: slugOf(first.name, taken), platform, ...first })
        return
      }
      for (const p of found) {
        const id = slugOf(p.name, taken)
        taken.push(id)
        await saveDashboard(platform, { id, platform, ...p })
      }
      await reloadDashboards(null)
      setDashboardInfo(t('dashboard.imported', { count: found.length }))
    } catch (err) {
      setDashboardError(
        t('dashboard.importFailed', { reason: err instanceof Error ? err.message : String(err) }),
      )
    }
  }
  const importButton = (
    <>
      <Button size="small" onClick={() => importInput.current?.click()}>
        {t('dashboard.import')}
      </Button>
      <input
        ref={importInput}
        type="file"
        accept="application/json,.json"
        hidden
        data-testid="dashboard-import"
        onChange={(e) => {
          const file = e.target.files?.[0]
          e.target.value = ''
          if (file) void importDashboards(file)
        }}
      />
    </>
  )

  const removeDashboard = async (d: Dashboard) => {
    if (!globalThis.confirm(t('dashboard.confirmDelete', { name: d.name }))) return
    setDashboardError(null)
    try {
      await deleteDashboard(platform, d.id)
      await reloadDashboards(null)
    } catch (err) {
      setDashboardError(err instanceof Error ? err.message : 'unknown error')
    }
  }

  return (
    <>
      <AppBar position="static" color="default" elevation={0}>
        <Toolbar sx={{ gap: 2, flexWrap: 'wrap', rowGap: 1, py: 1 }}>
          <Stack direction="row" spacing={1.5} alignItems="center" sx={{ flexGrow: 1 }}>
            <Box component="img" src="/logo.png" alt="" sx={{ width: 40, height: 40 }} />
            <Stack>
              <Typography variant="h6" component="h1" sx={{ lineHeight: 1.2 }}>
                {t('app.name')}
              </Typography>
              <VersionTag />
            </Stack>
          </Stack>
          {platforms.length > 1 ? (
            <Select
              size="small"
              value={platform}
              onChange={(e) => setPlatform(String(e.target.value))}
            >
              {platforms.map((name) => (
                <MenuItem key={name} value={name}>
                  {name}
                </MenuItem>
              ))}
            </Select>
          ) : null}
          <ResourceBadge />
          <ThemeToggle />
          <NotificationControls />
          <LiveConnectionBadge />
          <LogoutButton />
        </Toolbar>

        <Tabs
          value={tab}
          onChange={(_, next: TabName) => setTab(next)}
          variant="scrollable"
          scrollButtons="auto"
          allowScrollButtonsMobile
          sx={{ px: 2 }}
        >
          <Tab value="dashboard" label={t('tabs.dashboard')} />
          <Tab value="charts" label={t('tabs.charts')} />
          <Tab
            value="alerts"
            label={
              <Badge badgeContent={loud} color="error">
                <span>{t('tabs.alerts')}</span>
              </Badge>
            }
          />
          <Tab value="search" label={t('tabs.search')} />
          <Tab value="activity" label={t('tabs.activity')} />
          <Tab value="rules" label={t('tabs.rules')} />
          <Tab value="signals" label={t('tabs.signals')} />
          <Tab value="storage" label={t('tabs.storage')} />
          <Tab
            value="status"
            label={
              <Badge badgeContent={offline} color="error">
                <span>{t('tabs.status')}</span>
              </Badge>
            }
          />
          <Tab value="info" label={t('tabs.info')} />
          <Tab value="settings" label={t('tabs.settings')} />
          <Tab
            value="health"
            label={
              <Badge badgeContent={unknownIssues} color="error">
                <span>{t('tabs.health')}</span>
              </Badge>
            }
          />
        </Tabs>
      </AppBar>

      <Container maxWidth={false} sx={{ py: 3 }}>
        <Suspense fallback={<Busy />}>
          <Stack spacing={3}>
            {catalogueError ? <Alert severity="error">{catalogueError}</Alert> : null}
            <TabHint tab={tab} />
            {!catalogueError && signals.length === 0 && tab === 'charts' ? <ServerStatus /> : null}

            {tab === 'dashboard' && draft ? (
              <DashboardEditor
                dashboard={draft}
                platform={platform}
                specs={allSignals}
                onCancel={() => setDraft(null)}
                onSaved={(saved) => {
                  setDraft(null)
                  void reloadDashboards(saved.id)
                }}
              />
            ) : null}
            {tab === 'dashboard' && !draft && current ? (
              <Stack spacing={2}>
                <Stack direction="row" spacing={1} alignItems="center" flexWrap="wrap" useFlexGap>
                  {dashboards.length > 1 ? (
                    <Select
                      size="small"
                      value={current.id}
                      onChange={(e) => openDashboard(String(e.target.value))}
                      sx={{ minWidth: 200 }}
                    >
                      {dashboards.map((d) => (
                        <MenuItem key={d.id} value={d.id}>
                          {d.name}
                        </MenuItem>
                      ))}
                    </Select>
                  ) : null}
                  <Typography variant="body2" color="text.secondary">
                    {current.generated
                      ? t('dashboard.generated')
                      : current.readOnly
                        ? t('dashboard.shipped')
                        : current.createdBy
                          ? t('dashboard.by', { name: current.createdBy })
                          : ''}
                  </Typography>
                  {!current.readOnly ? (
                    <Button size="small" onClick={() => setDraft(structuredClone(current))}>
                      {t('dashboard.edit')}
                    </Button>
                  ) : null}
                  <Button size="small" onClick={() => newDashboard(current)}>
                    {t('dashboard.duplicate')}
                  </Button>
                  <Button size="small" onClick={() => newDashboard()}>
                    {t('dashboard.new')}
                  </Button>
                  <Button
                    size="small"
                    onClick={() => download(exportFileName(current), exportText([current]))}
                  >
                    {t('dashboard.export')}
                  </Button>
                  <Button
                    size="small"
                    onClick={() => download(exportAllFileName(platform), exportText(dashboards))}
                  >
                    {t('dashboard.exportAll')}
                  </Button>
                  {importButton}
                  <CopyToPlatformButton
                    dashboard={current}
                    platforms={platforms}
                    onCopied={(to) => {
                      setDashboardError(null)
                      setCopied({ name: current.name, from: platform, to })
                    }}
                    onError={(reason) => {
                      setCopied(null)
                      setDashboardError(t('dashboard.copyFailed', { reason }))
                    }}
                  />
                  {!current.readOnly ? (
                    <Button
                      size="small"
                      color="error"
                      onClick={() => void removeDashboard(current)}
                    >
                      {t('dashboard.delete')}
                    </Button>
                  ) : null}
                </Stack>
                {dashboardError ? <Alert severity="error">{dashboardError}</Alert> : null}
                {dashboardInfo ? (
                  <Alert severity="success" onClose={() => setDashboardInfo(null)}>
                    {dashboardInfo}
                  </Alert>
                ) : null}
                {copied?.from === platform ? (
                  <Alert
                    severity="success"
                    onClose={() => setCopied(null)}
                    action={
                      <Button
                        color="inherit"
                        size="small"
                        onClick={() => {
                          setCopied(null)
                          setPlatform(copied.to)
                        }}
                      >
                        {t('dashboard.openPlatform', { platform: copied.to })}
                      </Button>
                    }
                  >
                    {t('dashboard.copied', { name: copied.name, platform: copied.to })}
                  </Alert>
                ) : null}
                <DashboardPage
                  dashboard={current}
                  platform={platform}
                  specs={allSignals}
                  onOpen={setTab}
                  filterable
                />
              </Stack>
            ) : null}
            {tab === 'dashboard' && !draft && !current ? (
              <Stack spacing={1} alignItems="flex-start">
                <Stack direction="row" spacing={1}>
                  <Button onClick={() => newDashboard()}>{t('dashboard.new')}</Button>
                  {importButton}
                </Stack>
                {dashboardError ? <Alert severity="error">{dashboardError}</Alert> : null}
              </Stack>
            ) : null}
            {tab === 'charts' ? <ChartsPage signals={signals} /> : null}
            {tab === 'alerts' ? <AlertsPanel platform={platform} /> : null}
            {tab === 'status' ? (
              <Stack spacing={4}>
                <StatusTable platform={platform} />
                <ServerStats />
                <Box>
                  <Typography variant="subtitle1" gutterBottom>
                    {t('settings.viewers')}
                  </Typography>
                  <ViewersTable />
                </Box>
                <Box>
                  <Typography variant="subtitle1" gutterBottom>
                    {t('settings.visits')}
                  </Typography>
                  <VisitsTable />
                </Box>
              </Stack>
            ) : null}
            {tab === 'search' ? <SearchPanel platform={platform} /> : null}
            {tab === 'activity' ? <ActivityPanel /> : null}
            {tab === 'rules' ? <RulesPanel platform={platform} /> : null}
            {tab === 'signals' ? <SignalsPage platform={platform} specs={allSignals} /> : null}
            {tab === 'storage' ? <StoragePanel /> : null}
            {tab === 'info' ? <InfoPanel platform={platform} /> : null}
            {tab === 'settings' ? <SettingsPanel /> : null}
            {tab === 'health' ? <HealthPanel /> : null}
          </Stack>
        </Suspense>
      </Container>

      <NoticeBanner notice={notice} onDismiss={dismissNotice} />
    </>
  )
}
