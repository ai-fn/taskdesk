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

// 401 → try refresh once, then retry the original request.
// Subscriber queue prevents multiple parallel 401s from each calling
// /refresh independently (which would invalidate the rotated token).
let isRefreshing = false
let refreshSubscribers: Array<(token: string) => void> = []

api.interceptors.response.use(
  (response) => response,
  async (error: unknown) => {
    if (!axios.isAxiosError(error)) return Promise.reject(error)

    const cfg = error.config as AxiosRequestConfig & { _retry?: boolean }
    if (error.response?.status !== 401 || cfg._retry) {
      return Promise.reject(error)
    }
    cfg._retry = true

    if (isRefreshing) {
      return new Promise<unknown>((resolve) => {
        refreshSubscribers.push((token) => {
          if (cfg.headers) cfg.headers['Authorization'] = `Bearer ${token}`
          resolve(api(cfg))
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
      refreshSubscribers.forEach((cb) => cb(data.access_token))
      if (cfg.headers) cfg.headers['Authorization'] = `Bearer ${data.access_token}`
      return api(cfg)
    } catch {
      setToken(null)
      _onUnauthenticated()
      return Promise.reject(error)
    } finally {
      isRefreshing = false
      refreshSubscribers = []
    }
  },
)
