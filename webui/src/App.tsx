import { Navigate, Outlet, Route, Routes, useLocation } from 'react-router-dom'
import { useAuth } from './auth'
import DashboardPage from './pages/DashboardPage'
import LoginPage from './pages/LoginPage'
import OverviewPage from './pages/OverviewPage'
import SettingsPage from './pages/SettingsPage'

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
        <Route path="/" element={<DashboardPage />}>
          <Route index element={<OverviewPage />} />
          <Route path="settings" element={<SettingsPage />} />
        </Route>
      </Route>

      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  )
}
