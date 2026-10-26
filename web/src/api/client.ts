import axios from 'axios'
import type { AxiosRequestConfig } from 'axios'

// Module-level token store — lets the axios interceptor read the current
// token synchronously without stale-closure issues.
let _token: string | null = null
export const getToken = () => _token
export const setToken = (t: string | null) => {
  _token = t
}

// Injected by AuthProvider so the interceptor can navigate to /login when
// the refresh token is expired. Interceptors run outside React so
// useNavigate() can't be called there directly.
let _onUnauthenticated: () => void = () => {}
export const setOnUnauthenticated = (fn: () => void) => {
  _onUnauthenticated = fn
}

export const api = axios.create({
  baseURL: '/',
  withCredentials: true, // always send cookies (needed for refresh_token)
})

// Attach the current access token to every request.
api.interceptors.request.use((config) => {
  const token = getToken()
  if (token) {
    config.headers.Authorization = `Bearer ${token}`
  }
  return config
})

// 402 → user hit a pro-only endpoint. Dispatch an event so the UI can show the paywall.
// The PaywallRequired event bubbles up to App.tsx which renders a global PaywallModal.
export const PAYWALL_EVENT = 'plan:required'

// 401 → try refresh once, then retry the original request.
// Subscriber queue prevents multiple parallel 401s from each calling
// /refresh independently (which would invalidate the rotated token).
let isRefreshing = false
type Subscriber = { onSuccess: (token: string) => void; onFailure: (err: unknown) => void }
let refreshSubscribers: Subscriber[] = []

// Credential endpoints return 401 for bad credentials or a missing/expired refresh cookie, not for an
// expired access token. Refreshing for them is pointless, and the failure path would navigate
// away from the public page (/signup, /forgot-password) the visitor is on.
const AUTH_ENDPOINTS = [
  '/api/v1/auth/login',
  '/api/v1/auth/signup',
  '/api/v1/auth/refresh',
  '/api/v1/auth/logout',
  '/api/v1/auth/password/forgot',
  '/api/v1/auth/password/reset',
]

api.interceptors.response.use(
  (response) => response,
  async (error: unknown) => {
    if (!axios.isAxiosError(error)) return Promise.reject(error)

    const cfg = error.config as AxiosRequestConfig & { _retry?: boolean }

    // Pro-only endpoint hit by free user → fire paywall event
    if (error.response?.status === 402) {
      window.dispatchEvent(new CustomEvent(PAYWALL_EVENT))
      return Promise.reject(error)
    }

    const isAuthEndpoint = AUTH_ENDPOINTS.some((p) => cfg.url?.includes(p))
    if (error.response?.status !== 401 || cfg._retry || isAuthEndpoint) {
      return Promise.reject(error)
    }
    cfg._retry = true

    if (isRefreshing) {
      return new Promise<unknown>((resolve, reject) => {
        refreshSubscribers.push({
          onSuccess: (token) => {
            if (cfg.headers) cfg.headers['Authorization'] = `Bearer ${token}`
            resolve(api(cfg))
          },
          onFailure: (err) => reject(err),
        })
      })
    }

    isRefreshing = true
    try {
      const { data } = await axios.post<{ access_token: string }>(
        '/api/v1/auth/refresh',
        {},
        { withCredentials: true },
      )
      setToken(data.access_token)
      refreshSubscribers.forEach((s) => s.onSuccess(data.access_token))
      if (cfg.headers) cfg.headers['Authorization'] = `Bearer ${data.access_token}`
      return api(cfg)
    } catch (refreshError) {
      refreshSubscribers.forEach((s) => s.onFailure(refreshError))
      setToken(null)
      _onUnauthenticated()
      return Promise.reject(error)
    } finally {
      isRefreshing = false
      refreshSubscribers = []
    }
  },
)
