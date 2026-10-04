import '@fontsource/roboto/latin-400.css'
import '@fontsource/roboto/latin-500.css'
import { lazy, StrictMode, Suspense } from 'react'
import { createRoot } from 'react-dom/client'

import './i18n'
import { LoginGate } from './auth/LoginGate'
import { ThemeModeProvider } from './theme/ThemeModeProvider'

const App = lazy(() => import('./app/App').then((m) => ({ default: m.App })))
const DisplayPage = lazy(() =>
  import('./display/DisplayPage').then((m) => ({ default: m.DisplayPage })),
)

const container = document.getElementById('root')
if (!container) throw new Error('root element is missing')

// The wall display is its own page rather than a mode of the dashboard: no
// menus, no dialogs, and no login form to block a screen nobody is standing
// at.
const params = new URLSearchParams(globalThis.location.search)
const isDisplay = params.get('display') === '1' || params.has('token')

createRoot(container).render(
  <StrictMode>
    <ThemeModeProvider>
      <Suspense fallback={null}>
        {isDisplay ? (
          <DisplayPage />
        ) : (
          <LoginGate>
            <App />
          </LoginGate>
        )}
      </Suspense>
    </ThemeModeProvider>
  </StrictMode>,
)
