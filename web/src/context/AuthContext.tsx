import { createContext, useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import * as authApi from '@/api/auth'
import { setToken, setOnUnauthenticated } from '@/api/client'

interface AuthState {
  accessToken: string | null
  userId: string | null
  isInitializing: boolean
}

interface AuthContextValue extends Omit<AuthState, 'accessToken'> {
  isAuthenticated: boolean
  login: (email: string, password: string) => Promise<void>
  signup: (email: string, password: string, name: string) => Promise<void>
  logout: () => Promise<void>
}

export const AuthContext = createContext<AuthContextValue | null>(null)

export function AuthProvider({ children }: { children: React.ReactNode }) {
  const [state, setState] = useState<AuthState>({
    accessToken: null,
    userId: null,
    isInitializing: true,
  })
  const navigate = useNavigate()
  // Avoid registering navigate on every render
  const navigateRef = useRef(navigate)
  navigateRef.current = navigate

  // Wire the interceptor's unauthenticated callback once on mount.
  useEffect(() => {
    setOnUnauthenticated(() => navigateRef.current('/login'))
  }, [])

  // On mount: try to restore the session using the HttpOnly refresh cookie.
  useEffect(() => {
    authApi
      .refresh()
      .then((res) => {
        setToken(res.access_token)
        // We don't have userId from the refresh response — that's fine, the
        // access token itself carries the claims. We only need userId for display.
        setState({ accessToken: res.access_token, userId: null, isInitializing: false })
      })
      .catch(() => {
        setState({ accessToken: null, userId: null, isInitializing: false })
      })
  }, [])

  async function login(email: string, password: string) {
    const res = await authApi.login(email, password)
    setToken(res.access_token)
    setState({ accessToken: res.access_token, userId: res.user_id, isInitializing: false })
  }

  async function signup(email: string, password: string, name: string) {
    const res = await authApi.signup(email, password, name)
    setToken(res.access_token)
    setState({ accessToken: res.access_token, userId: res.user_id, isInitializing: false })
  }

  async function logout() {
    try {
      await authApi.logout()
    } finally {
      setToken(null)
      setState({ accessToken: null, userId: null, isInitializing: false })
      navigateRef.current('/login')
    }
  }

  return (
    <AuthContext.Provider
      value={{
        userId: state.userId,
        isInitializing: state.isInitializing,
        isAuthenticated: state.accessToken !== null,
        login,
        signup,
        logout,
      }}
    >
      {children}
    </AuthContext.Provider>
  )
}
