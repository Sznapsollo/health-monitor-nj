import ContentCopyIcon from '@mui/icons-material/ContentCopy'
import DeleteIcon from '@mui/icons-material/Delete'
import Alert from '@mui/material/Alert'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Checkbox from '@mui/material/Checkbox'
import FormControl from '@mui/material/FormControl'
import FormControlLabel from '@mui/material/FormControlLabel'
import IconButton from '@mui/material/IconButton'
import InputLabel from '@mui/material/InputLabel'
import MenuItem from '@mui/material/MenuItem'
import Select from '@mui/material/Select'
import Stack from '@mui/material/Stack'
import Table from '@mui/material/Table'
import TableBody from '@mui/material/TableBody'
import TableCell from '@mui/material/TableCell'
import TableHead from '@mui/material/TableHead'
import TableRow from '@mui/material/TableRow'
import TextField from '@mui/material/TextField'
import Tooltip from '@mui/material/Tooltip'
import Typography from '@mui/material/Typography'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import {
  createDisplayToken,
  fetchDisplayTokens,
  revokeDisplayToken,
  updateDisplayOptions,
  sendMessage,
  type DisplayOptions,
  type DisplayToken,
} from '../api/session'
import { fetchDashboards, type Dashboard } from '../api/dashboards'
import { requestPermission } from '../alerts/notifications'
import { languages } from '../i18n'
import { useAlertStore } from '../store/useAlertStore'
import { useMonitorStore } from '../store/useMonitorStore'
import { useThemeMode } from '../theme/useThemeMode'
import type { ThemeMode } from '../theme/tokens'
import { DisplayOptionsEditor } from './DisplayOptionsEditor'

/**
 * Settings: the per-viewer choices (language, theme, notifications) and the
 * actions that used to live in the old "Akcje" menu.
 */
export function SettingsPanel() {
  const { t, i18n } = useTranslation()
  const { mode, setMode } = useThemeMode()
  const { notifications, setNotifications } = useAlertStore()

  const platforms = useMonitorStore((s) => s.platforms)
  const currentPlatform = useMonitorStore((s) => s.platform)

  const [tokens, setTokens] = useState<DisplayToken[]>([])
  const [newToken, setNewToken] = useState<DisplayToken | null>(null)
  const [newOptions, setNewOptions] = useState<DisplayOptions>({ showLive: true })
  const [tokenName, setTokenName] = useState('')
  // A screen is paired with one arrangement: that is what it will show, and
  // there is no way to pick another from the screen itself.
  const [tokenPlatform, setTokenPlatform] = useState(currentPlatform)
  const [tokenDashboard, setTokenDashboard] = useState('')
  const [boards, setBoards] = useState<Dashboard[]>([])
  const [message, setMessage] = useState('')
  const [notice, setNotice] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    if (!tokenPlatform) setTokenPlatform(currentPlatform)
  }, [currentPlatform, tokenPlatform])

  // The dashboards a screen can be pointed at are the ones this platform
  // defines; there is nothing else for it to show.
  useEffect(() => {
    if (!tokenPlatform) return
    const controller = new AbortController()
    fetchDashboards(tokenPlatform, controller.signal)
      .then((list) => {
        setBoards(list)
        setTokenDashboard(list.find((d) => d.default)?.id ?? list[0]?.id ?? '')
      })
      .catch(() => {
        setBoards([])
        setTokenDashboard('')
      })
    return () => controller.abort()
  }, [tokenPlatform])

  useEffect(() => {
    const controller = new AbortController()
    fetchDisplayTokens(controller.signal)
      .then(setTokens)
      .catch(() => setTokens([]))
    return () => controller.abort()
  }, [])

  const run = async (fn: () => Promise<void>) => {
    setError(null)
    try {
      await fn()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'unknown error')
    }
  }

  return (
    <Stack spacing={4}>
      {error ? <Alert severity="error">{error}</Alert> : null}
      {notice ? <Alert severity="success">{notice}</Alert> : null}

      <Box>
        <Typography variant="subtitle1" gutterBottom>
          {t('settings.thisBrowser')}
        </Typography>
        <Stack direction="row" spacing={2} flexWrap="wrap" useFlexGap>
          <FormControl size="small" sx={{ minWidth: 160 }}>
            <InputLabel id="settings-language">{t('settings.language')}</InputLabel>
            <Select
              labelId="settings-language"
              label={t('settings.language')}
              value={languages.includes(i18n.language as never) ? i18n.language : 'en'}
              onChange={(e) => void i18n.changeLanguage(String(e.target.value))}
            >
              {languages.map((code) => (
                <MenuItem key={code} value={code}>
                  {t(`settings.languages.${code}`)}
                </MenuItem>
              ))}
            </Select>
          </FormControl>

          <FormControl size="small" sx={{ minWidth: 160 }}>
            <InputLabel id="settings-theme">{t('settings.theme')}</InputLabel>
            <Select
              labelId="settings-theme"
              label={t('settings.theme')}
              value={mode}
              onChange={(e) => setMode(e.target.value as ThemeMode)}
            >
              <MenuItem value="light">{t('settings.theme_light')}</MenuItem>
              <MenuItem value="dark">{t('settings.theme_dark')}</MenuItem>
              <MenuItem value="system">{t('settings.theme_system')}</MenuItem>
            </Select>
          </FormControl>

          <FormControlLabel
            control={
              <Checkbox
                checked={notifications.sound}
                onChange={(e) => setNotifications({ ...notifications, sound: e.target.checked })}
              />
            }
            label={t('settings.sound')}
          />
          <FormControlLabel
            control={
              <Checkbox
                checked={notifications.browser}
                onChange={(e) =>
                  void (async () => {
                    const on = e.target.checked && (await requestPermission())
                    setNotifications({ ...notifications, browser: on })
                  })()
                }
              />
            }
            label={t('settings.browserNotifications')}
          />
        </Stack>
      </Box>

      <Box>
        <Typography variant="subtitle1" gutterBottom>
          {t('settings.message')}
        </Typography>
        <Stack direction="row" spacing={1} alignItems="center" flexWrap="wrap" useFlexGap>
          <TextField
            size="small"
            label={t('settings.messageText')}
            value={message}
            onChange={(e) => setMessage(e.target.value)}
            sx={{ minWidth: 320 }}
          />
          <Button
            size="small"
            variant="outlined"
            disabled={!message.trim()}
            onClick={() =>
              void run(async () => {
                await sendMessage(message.trim(), false)
                setNotice(t('settings.messageSent'))
                setMessage('')
              })
            }
          >
            {t('settings.send')}
          </Button>
        </Stack>
      </Box>

      <Box>
        <Typography variant="subtitle1" gutterBottom>
          {t('settings.displays')}
        </Typography>
        <Typography variant="body2" color="text.secondary" gutterBottom>
          {t('settings.displaysHelp')}
        </Typography>

        {newToken?.secret ? (
          <Alert severity="info" sx={{ mb: 2 }}>
            <Stack direction="row" spacing={1} alignItems="center" flexWrap="wrap" useFlexGap>
              <Box component="code" sx={{ wordBreak: 'break-all' }}>
                {displayUrl(newToken.secret)}
              </Box>
              <Tooltip title={t('settings.copy')}>
                <IconButton
                  size="small"
                  aria-label={t('settings.copy')}
                  onClick={() =>
                    void globalThis.navigator?.clipboard?.writeText(
                      displayUrl(newToken.secret ?? ''),
                    )
                  }
                >
                  <ContentCopyIcon fontSize="small" />
                </IconButton>
              </Tooltip>
            </Stack>
            <Typography variant="caption">{t('settings.tokenOnce')}</Typography>
          </Alert>
        ) : null}

        <Stack
          direction="row"
          spacing={1}
          alignItems="center"
          flexWrap="wrap"
          useFlexGap
          sx={{ mb: 2 }}
        >
          <TextField
            size="small"
            label={t('settings.tokenName')}
            value={tokenName}
            onChange={(e) => setTokenName(e.target.value)}
          />
          {platforms.length > 1 ? (
            <FormControl size="small" sx={{ minWidth: 160 }}>
              <InputLabel id="display-platform">{t('settings.tokenPlatform')}</InputLabel>
              <Select
                labelId="display-platform"
                label={t('settings.tokenPlatform')}
                value={tokenPlatform}
                onChange={(e) => setTokenPlatform(String(e.target.value))}
              >
                {platforms.map((name) => (
                  <MenuItem key={name} value={name}>
                    {name}
                  </MenuItem>
                ))}
              </Select>
            </FormControl>
          ) : null}
          <FormControl size="small" sx={{ minWidth: 200 }} disabled={boards.length === 0}>
            <InputLabel id="display-dashboard">{t('settings.tokenDashboard')}</InputLabel>
            <Select
              labelId="display-dashboard"
              label={t('settings.tokenDashboard')}
              value={tokenDashboard}
              onChange={(e) => setTokenDashboard(String(e.target.value))}
            >
              {boards.map((board) => (
                <MenuItem key={board.id} value={board.id}>
                  {board.name}
                </MenuItem>
              ))}
            </Select>
          </FormControl>
          <DisplayOptionsEditor value={newOptions} onChange={setNewOptions} />
          <Button
            size="small"
            variant="outlined"
            disabled={!tokenName.trim() || !tokenDashboard}
            onClick={() =>
              void run(async () => {
                const created = await createDisplayToken(
                  tokenName.trim(),
                  tokenPlatform,
                  tokenDashboard,
                  newOptions,
                )
                setNewToken(created)
                setTokenName('')
                setTokens(await fetchDisplayTokens())
              })
            }
          >
            {t('settings.createToken')}
          </Button>
        </Stack>

        {boards.length === 0 ? (
          <Alert severity="info" sx={{ mb: 2 }}>
            {t('settings.noDashboards')}
          </Alert>
        ) : null}

        {tokens.length > 0 ? (
          <Table size="small">
            <TableHead>
              <TableRow>
                <TableCell align="right" sx={{ width: 48 }}>
                  {t('grid.number')}
                </TableCell>
                <TableCell>{t('settings.tokenName')}</TableCell>
                <TableCell>{t('settings.shows')}</TableCell>
                <TableCell>{t('settings.displayLook')}</TableCell>
                <TableCell>{t('settings.created')}</TableCell>
                <TableCell>{t('settings.lastUsed')}</TableCell>
                <TableCell align="right" />
              </TableRow>
            </TableHead>
            <TableBody>
              {tokens.map((token, n) => (
                <TableRow key={token.id}>
                  <TableCell align="right" sx={{ color: 'text.secondary' }}>
                    {n + 1}
                  </TableCell>
                  <TableCell>{token.name}</TableCell>
                  <TableCell>
                    {token.dashboard
                      ? (boards.find((b) => b.id === token.dashboard)?.name ?? token.dashboard)
                      : '—'}
                  </TableCell>
                  <TableCell>
                    <DisplayOptionsEditor
                      value={token.options ?? {}}
                      onChange={(options) =>
                        void run(async () => {
                          await updateDisplayOptions(token.id, options)
                          setTokens(await fetchDisplayTokens())
                        })
                      }
                    />
                  </TableCell>
                  <TableCell>{token.created.slice(0, 10)}</TableCell>
                  <TableCell>
                    {token.lastUsed ? token.lastUsed.slice(0, 16).replace('T', ' ') : '—'}
                  </TableCell>
                  <TableCell align="right">
                    <IconButton
                      size="small"
                      aria-label={t('settings.revoke')}
                      onClick={() =>
                        void run(async () => {
                          await revokeDisplayToken(token.id)
                          setTokens(await fetchDisplayTokens())
                        })
                      }
                    >
                      <DeleteIcon fontSize="small" />
                    </IconButton>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        ) : null}
      </Box>
    </Stack>
  )
}

function displayUrl(secret: string): string {
  const origin = globalThis.location?.origin ?? ''
  return `${origin}/?display=1&token=${secret}`
}
