import { createContext, useContext, useEffect, useState, type ReactNode } from 'react'

export type Theme = 'light' | 'dark' | 'system'

interface ThemeContextValue {
  theme: Theme
  setTheme: (t: Theme) => void
}

const ThemeContext = createContext<ThemeContextValue | null>(null)

function applyTheme(theme: Theme) {
  const root = document.documentElement
  const prefersDark = window.matchMedia('(prefers-color-scheme: dark)').matches
  if (theme === 'dark' || (theme === 'system' && prefersDark)) {
    root.classList.add('dark')
  } else {
    root.classList.remove('dark')
  }
}

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [theme, setThemeState] = useState<Theme>(() => {
    return (localStorage.getItem('theme') as Theme | null) ?? 'system'
  })

  // Apply theme whenever it changes
  useEffect(() => {
    applyTheme(theme)
  }, [theme])

  // Listen for OS-level theme changes
  useEffect(() => {
    if (theme !== 'system') return

    const mq = window.matchMedia('(prefers-color-scheme: dark)')

    // Standard event (works in Chrome/Firefox when browser propagates the change)
    const handleChange = () => applyTheme('system')
    mq.addEventListener('change', handleChange)

    // Fallback for GNOME/Linux: re-check on focus/visibility
    const handleVisibility = () => {
      if (document.visibilityState === 'visible') applyTheme('system')
    }
    const handleFocus = () => applyTheme('system')
    document.addEventListener('visibilitychange', handleVisibility)
    window.addEventListener('focus', handleFocus)

    // Last-resort polling for Linux setups where the mq change event never fires
    // (e.g. GTK-only theme switches that don't propagate to prefers-color-scheme)
    const poll = setInterval(() => applyTheme('system'), 2000)

    return () => {
      mq.removeEventListener('change', handleChange)
      document.removeEventListener('visibilitychange', handleVisibility)
      window.removeEventListener('focus', handleFocus)
      clearInterval(poll)
    }
  }, [theme])

  function setTheme(t: Theme) {
    localStorage.setItem('theme', t)
    setThemeState(t)
  }

  return <ThemeContext.Provider value={{ theme, setTheme }}>{children}</ThemeContext.Provider>
}

export function useTheme() {
  const ctx = useContext(ThemeContext)
  if (!ctx) throw new Error('useTheme must be used within ThemeProvider')
  return ctx
}
