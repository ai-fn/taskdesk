import type { ReactNode } from 'react'
import { useEffect, useRef, useState } from 'react'
import { Navigate, Outlet, Route, Routes } from 'react-router-dom'
import { AuthProvider } from '@/context/AuthContext'
import { PaywallModal } from '@/components/ui/PaywallModal'
import { PAYWALL_EVENT } from '@/api/client'
import { AppLayout } from '@/components/layout/AppLayout'
import { Spinner } from '@/components/ui/Spinner'
import { LoginPage } from '@/pages/LoginPage'
import { SignupPage } from '@/pages/SignupPage'
import { ForgotPasswordPage } from '@/pages/ForgotPasswordPage'
import { ListsHomePage } from '@/pages/ListsHomePage'
import { TasksPage } from '@/pages/TasksPage'
import { TaskDetailPage } from '@/pages/TaskDetailPage'
import { useAuth } from '@/hooks/useAuth'
import { AccountPage } from '@/pages/AccountPage'
import { UpgradePage } from '@/pages/UpgradePage'

function PublicRoute({ children }: { children: ReactNode }) {
  const { isInitializing, isAuthenticated } = useAuth()
  if (isInitializing) {
    return (
      <div className="flex min-h-screen items-center justify-center">
        <Spinner className="h-8 w-8 text-indigo-500" />
      </div>
    )
  }
  if (isAuthenticated) return <Navigate to="/" replace />
  return <>{children}</>
}

function ProtectedRoute() {
  const { isInitializing, isAuthenticated } = useAuth()
  if (isInitializing) {
    return (
      <div className="flex min-h-screen items-center justify-center">
        <Spinner className="h-8 w-8 text-indigo-500" />
      </div>
    )
  }
  if (!isAuthenticated) return <Navigate to="/login" replace />
  return <Outlet />
}

const PAYWALL_COOLDOWN_MS = 30_000

function GlobalPaywall() {
  const [open, setOpen] = useState(false)
  const closedAtRef = useRef<number>(0)
  useEffect(() => {
    const handler = () => {
      if (Date.now() - closedAtRef.current < PAYWALL_COOLDOWN_MS) return
      setOpen(true)
    }
    window.addEventListener(PAYWALL_EVENT, handler)
    return () => window.removeEventListener(PAYWALL_EVENT, handler)
  }, [])
  if (!open) return null
  return (
    <PaywallModal
      onClose={() => {
        closedAtRef.current = Date.now()
        setOpen(false)
      }}
    />
  )
}

export function App() {
  return (
    <AuthProvider>
      <Routes>
        <Route path="/login" element={<PublicRoute><LoginPage /></PublicRoute>} />
        <Route path="/signup" element={<PublicRoute><SignupPage /></PublicRoute>} />
        <Route path="/forgot-password" element={<PublicRoute><ForgotPasswordPage /></PublicRoute>} />
        <Route element={<ProtectedRoute />}>
          <Route element={<AppLayout />}>
            <Route path="/" element={<ListsHomePage />} />
            <Route path="/account" element={<AccountPage />} />
            <Route path="/upgrade" element={<UpgradePage />} />
            <Route path="/tasks/detail/:taskId" element={<TaskDetailPage />} />
            <Route path="/tasks/:listId" element={<TasksPage />} />
          </Route>
        </Route>
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
      <GlobalPaywall />
    </AuthProvider>
  )
}
