import { useRef, useState } from 'react'
import { Link, Outlet, useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useAuth } from '@/hooks/useAuth'
import { ThemeToggle } from '@/components/ui/ThemeToggle'
import { useClickOutside } from '@/hooks/useClickOutside'

export function AppLayout() {
  const { t } = useTranslation()
  const { logout, email } = useAuth()
  const navigate = useNavigate()
  const [open, setOpen] = useState(false)
  const menuRef = useRef<HTMLDivElement>(null)

  useClickOutside(menuRef, () => setOpen(false))

  const initials = email ? email[0].toUpperCase() : '?'
  const displayName = email ?? 'Account'

  return (
    <div className="min-h-screen bg-gray-100 dark:bg-gray-950">
      <header className="bg-white dark:bg-gray-900 shadow-sm dark:shadow-gray-800/20">
        <div className="mx-auto flex max-w-3xl items-center justify-between px-4 py-3">
          <Link to="/" className="text-lg font-semibold text-indigo-600 dark:text-indigo-400 hover:opacity-80 transition-opacity">{t('layout.appName')}</Link>
          <div className="flex items-center gap-3">
            <ThemeToggle />
            <div className="relative" ref={menuRef}>
              <button
                onClick={() => setOpen((v) => !v)}
                className="flex items-center gap-2 rounded-md px-2 py-1 hover:bg-gray-100 dark:hover:bg-gray-800 transition-colors focus:outline-none"
                aria-haspopup="true"
                aria-expanded={open}
              >
                <div className="flex h-7 w-7 items-center justify-center rounded-full bg-indigo-100 dark:bg-indigo-900 text-xs font-semibold text-indigo-700 dark:text-indigo-300">
                  {initials}
                </div>
                <span className="hidden text-sm text-gray-600 dark:text-gray-300 sm:block">{displayName}</span>
              </button>

              {open && (
                <div className="absolute right-0 top-full mt-1 w-48 rounded-lg bg-white dark:bg-gray-800 shadow-lg border border-gray-200 dark:border-gray-700 py-1 z-50 animate-dropdown-in origin-top">
                  <button
                    className="flex w-full items-center gap-2 px-4 py-2 text-sm text-gray-700 dark:text-gray-200 hover:bg-gray-100 dark:hover:bg-gray-700 transition-colors"
                    onClick={() => { setOpen(false); navigate('/account') }}
                  >
                    {t('layout.accountSettings')}
                  </button>
                  <div className="my-1 border-t border-gray-200 dark:border-gray-700" />
                  <button
                    className="flex w-full items-center gap-2 px-4 py-2 text-sm text-red-600 dark:text-red-400 hover:bg-red-50 dark:hover:bg-red-900/30 transition-colors"
                    onClick={() => { setOpen(false); void logout() }}
                  >
                    {t('layout.signOut')}
                  </button>
                </div>
              )}
            </div>
          </div>
        </div>
      </header>
      <main className="mx-auto max-w-3xl px-4 py-6">
        <Outlet />
      </main>
    </div>
  )
}
