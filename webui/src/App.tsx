import { lazy } from 'react'
import { Navigate, Outlet, Route, Routes, useLocation } from 'react-router-dom'
import { useAuth } from './auth'
import DashboardPage from './pages/DashboardPage'
import LoginPage from './pages/LoginPage'
import { TailscaleProvider } from './tailscale'
import { DeviceProvider } from './device'

const OVERVIEW_PAGE = lazy(() => import('./pages/OverviewPage'))
const SETTINGS_PAGE = lazy(() => import('./pages/SettingsPage'))
const NETWORK_PAGE = lazy(() => import('./pages/NetworkPage'))
const ACCESS_CONTROL_PAGE = lazy(() => import('./pages/AccessControlPage'))
const TAILSCALE_SETUP_PAGE = lazy(() => import('./pages/TailscaleSetupPage'))

function ProtectedRoute() {
  const { isAuthenticated } = useAuth()
  const location = useLocation()

  return isAuthenticated ? (
    <Outlet />
  ) : (
    <Navigate to="/login" replace state={{ from: location }} />
  )
}

function PublicOnlyRoute() {
  const { isAuthenticated } = useAuth()
  const location = useLocation()
  const previousPath = (
    location.state as { from?: { pathname?: string } } | null
  )?.from?.pathname

  return isAuthenticated ? <Navigate to={previousPath || '/'} replace /> : <Outlet />
}

export default function App() {
  return (
    <Routes>
      <Route element={<PublicOnlyRoute />}>
        <Route path="/login" element={<LoginPage />} />
      </Route>

      <Route element={<ProtectedRoute />}>
        <Route path="/" element={<TailscaleProvider><DeviceProvider><DashboardPage /></DeviceProvider></TailscaleProvider>}>
          <Route index element={<OVERVIEW_PAGE />} />
          <Route path="settings" element={<SETTINGS_PAGE />} />
          <Route path="network" element={<NETWORK_PAGE />} />
          <Route path="access-control" element={<ACCESS_CONTROL_PAGE />} />
          <Route path="tailscale-setup" element={<TAILSCALE_SETUP_PAGE />} />
          <Route path="tailscale-setup/:guide" element={<TAILSCALE_SETUP_PAGE />} />
        </Route>
      </Route>

      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  )
}
