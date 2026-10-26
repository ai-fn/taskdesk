import { createContext, useCallback, useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useQueryClient } from '@tanstack/react-query'
import { planKeys, taskKeys } from '@/api/queryKeys'
import * as authApi from '@/api/auth'
import * as accountApi from '@/api/account'
import * as subscriptionApi from '@/api/subscription'
import { setToken, setOnUnauthenticated } from '@/api/client'
import i18n from '@/i18n/config'
import { browserTimeZone } from '@/lib/dateTime'

// Decode the JWT payload (display-only — never used for auth decisions).
function jwtEmail(token: string): string | null {
  try {
    const b64 = token.split('.')[1].replace(/-/g, '+').replace(/_/g, '/')
    const payload = JSON.parse(atob(b64))
    return typeof payload.email === 'string' ? payload.email : null
  } catch {
    return null
  }
}

// Keeps the account zone (server-side "today", plan timing) in step with the browser.
function syncTimeZone(current: string | undefined, onChanged: () => void): void {
  const zone = browserTimeZone()
  if (!zone || zone === current) return
  accountApi.updateTimeZone(zone).then(onChanged).catch(() => { /* non-fatal: the old zone stays */ })
}

interface AuthState {
  accessToken: string | null
  userId: string | null
  email: string | null
  emailVerified: boolean
  isInitializing: boolean
  plan: 'free' | 'pro'
}

interface AuthContextValue extends Omit<AuthState, 'accessToken'> {
  isAuthenticated: boolean
  login: (email: string, password: string) => Promise<void>
  signup: (email: string, password: string, name: string) => Promise<void>
  logout: () => Promise<void>
  setEmailVerified: () => void
  refreshPlan: () => Promise<void>
}

export const AuthContext = createContext<AuthContextValue | null>(null)

export function AuthProvider({ children }: { children: React.ReactNode }) {
  const [state, setState] = useState<AuthState>({
    accessToken: null,
    userId: null,
    email: null,
    emailVerified: false,
    isInitializing: true,
    plan: 'free',
  })
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  // Fetches that raced the zone update used the old zone; refetch them.
  const syncZone = useCallback((current?: string) => syncTimeZone(current, () => {
    void queryClient.invalidateQueries({ queryKey: taskKeys.all })
    void queryClient.invalidateQueries({ queryKey: planKeys.latest() })
  }), [queryClient])
  // Avoid registering navigate on every render
  const navigateRef = useRef(navigate)
  navigateRef.current = navigate

  // Wire the interceptor's unauthenticated callback once on mount.
  useEffect(() => {
    setOnUnauthenticated(() => navigateRef.current('/login'))
  }, [])

  async function fetchPlan() {
    try {
      const sub = await subscriptionApi.getSubscription()
      setState((s) => ({ ...s, plan: sub.plan }))
    } catch {
      // Non-fatal — default stays 'free'
    }
  }

  // On mount: try to restore the session using the HttpOnly refresh cookie.
  useEffect(() => {
    authApi
      .refresh()
      .then((res) => {
        setToken(res.access_token)
        setState({ accessToken: res.access_token, userId: null, email: jwtEmail(res.access_token), emailVerified: false, isInitializing: false, plan: 'free' })
        // Reconcile language + email_verified from DB.
        accountApi.getMe().then((me) => {
          if (me.language && me.language !== i18n.language) i18n.changeLanguage(me.language)
          syncZone(me.time_zone)
          setState((s) => ({ ...s, emailVerified: me.email_verified }))
        }).catch(() => { /* non-fatal */ })
        fetchPlan()
      })
      .catch(() => {
        setState({ accessToken: null, userId: null, email: null, emailVerified: false, isInitializing: false, plan: 'free' })
      })
  }, [syncZone])

  async function login(email: string, password: string) {
    const res = await authApi.login(email, password)
    setToken(res.access_token)
    setState({ accessToken: res.access_token, userId: res.user_id, email: jwtEmail(res.access_token), emailVerified: false, isInitializing: false, plan: 'free' })
    accountApi.getMe().then((me) => {
      if (me.language && me.language !== i18n.language) i18n.changeLanguage(me.language)
      syncZone(me.time_zone)
      setState((s) => ({ ...s, emailVerified: me.email_verified }))
    }).catch(() => { /* non-fatal */ })
    fetchPlan()
  }

  async function signup(email: string, password: string, name: string) {
    const res = await authApi.signup(email, password, name)
    setToken(res.access_token)
    setState({ accessToken: res.access_token, userId: res.user_id, email: jwtEmail(res.access_token), emailVerified: false, isInitializing: false, plan: 'free' })
    syncZone()
    // New signups start on free plan — no need to fetch
  }

  async function logout() {
    try {
      await authApi.logout()
    } finally {
      setToken(null)
      setState({ accessToken: null, userId: null, email: null, emailVerified: false, isInitializing: false, plan: 'free' })
      navigateRef.current('/login')
    }
  }

  function setEmailVerified() {
    setState((s) => ({ ...s, emailVerified: true }))
  }

  async function refreshPlan() {
    await fetchPlan()
  }

  return (
    <AuthContext.Provider
      value={{
        userId: state.userId,
        email: state.email,
        emailVerified: state.emailVerified,
        isInitializing: state.isInitializing,
        isAuthenticated: state.accessToken !== null,
        plan: state.plan,
        login,
        signup,
        logout,
        setEmailVerified,
        refreshPlan,
      }}
    >
      {children}
    </AuthContext.Provider>
  )
}
