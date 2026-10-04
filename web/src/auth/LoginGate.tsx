import VisibilityIcon from '@mui/icons-material/Visibility'
import VisibilityOffIcon from '@mui/icons-material/VisibilityOff'
import Alert from '@mui/material/Alert'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import IconButton from '@mui/material/IconButton'
import InputAdornment from '@mui/material/InputAdornment'
import Paper from '@mui/material/Paper'
import Stack from '@mui/material/Stack'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import { useEffect, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { fetchSession, login, type SessionInfo } from '../api/session'

/**
 * Shows the dashboard once there is a session, and a login form when the
 * server asks for one. A server with no password configured never shows it.
 */
export function LoginGate({ children }: { children: ReactNode }) {
  const { t } = useTranslation()
  const [session, setSession] = useState<SessionInfo | null>(null)
  const [name, setName] = useState('')
  const [password, setPassword] = useState('')
  const [showPassword, setShowPassword] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [unreachable, setUnreachable] = useState(false)
  const [attempt, setAttempt] = useState(0)

  useEffect(() => {
    const controller = new AbortController()
    fetchSession(controller.signal)
      .then((s) => {
        setUnreachable(false)
        setSession(s)
      })
      .catch(() => {
        if (!controller.signal.aborted) setUnreachable(true)
      })
    return () => controller.abort()
  }, [attempt])

  if (!session) {
    if (!unreachable) return null
    return (
      <Box sx={{ display: 'grid', placeItems: 'center', minHeight: '100vh', p: 2 }}>
        <Alert
          severity="error"
          action={
            <Button
              color="inherit"
              size="small"
              onClick={() => {
                setUnreachable(false)
                setAttempt((n) => n + 1)
              }}
            >
              {t('login.retry')}
            </Button>
          }
        >
          {t('login.unreachable')}
        </Alert>
      </Box>
    )
  }
  if (!session.required || session.authenticated) return <>{children}</>

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      setSession(await login(name, password))
    } catch (err) {
      setError(err instanceof Error ? err.message : 'unknown error')
    } finally {
      setBusy(false)
    }
  }

  return (
    <Box sx={{ display: 'grid', placeItems: 'center', minHeight: '100vh', p: 2 }}>
      <Paper variant="outlined" sx={{ p: 3, width: '100%', maxWidth: 360 }}>
        <form onSubmit={(e) => void submit(e)}>
          <Stack spacing={2}>
            <Typography variant="h6" component="h1">
              {t('app.name')}
            </Typography>
            <TextField
              size="small"
              label={t('login.name')}
              value={name}
              onChange={(e) => setName(e.target.value)}
              autoFocus
              helperText={t('login.nameHelp')}
            />
            <TextField
              size="small"
              type={showPassword ? 'text' : 'password'}
              label={t('login.password')}
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              autoComplete="current-password"
              slotProps={{
                input: {
                  endAdornment: (
                    <InputAdornment position="end">
                      <IconButton
                        size="small"
                        edge="end"
                        aria-label={t(showPassword ? 'login.hidePassword' : 'login.showPassword')}
                        aria-pressed={showPassword}
                        onClick={() => setShowPassword((s) => !s)}
                        onMouseDown={(e) => e.preventDefault()}
                      >
                        {showPassword ? (
                          <VisibilityOffIcon fontSize="small" />
                        ) : (
                          <VisibilityIcon fontSize="small" />
                        )}
                      </IconButton>
                    </InputAdornment>
                  ),
                },
              }}
            />
            {error ? <Alert severity="error">{error}</Alert> : null}
            <Button type="submit" variant="contained" disabled={busy || !password}>
              {t('login.submit')}
            </Button>
          </Stack>
        </form>
      </Paper>
    </Box>
  )
}
