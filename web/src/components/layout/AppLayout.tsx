import { Outlet } from 'react-router-dom'
import { useAuth } from '@/hooks/useAuth'
import { Button } from '@/components/ui/Button'
import { ThemeToggle } from '@/components/ui/ThemeToggle'

export function AppLayout() {
  const { logout, email } = useAuth()

  const initials = email ? email[0].toUpperCase() : '?'
  const displayName = email ?? 'Account'

  return (
    <div className="min-h-screen bg-gray-100 dark:bg-gray-950">
      <header className="bg-white dark:bg-gray-900 shadow-sm dark:shadow-gray-800/20">
        <div className="mx-auto flex max-w-3xl items-center justify-between px-4 py-3">
          <span className="text-lg font-semibold text-indigo-600 dark:text-indigo-400">Todo</span>
          <div className="flex items-center gap-3">
            <ThemeToggle />
            <div className="flex items-center gap-2">
              <div className="flex h-7 w-7 items-center justify-center rounded-full bg-indigo-100 dark:bg-indigo-900 text-xs font-semibold text-indigo-700 dark:text-indigo-300">
                {initials}
              </div>
              <span className="hidden text-sm text-gray-600 dark:text-gray-300 sm:block">{displayName}</span>
            </div>
            <Button variant="ghost" onClick={() => void logout()}>
              Sign out
            </Button>
          </div>
        </div>
      </header>
      <main className="mx-auto max-w-3xl px-4 py-6">
        <Outlet />
      </main>
    </div>
  )
}
