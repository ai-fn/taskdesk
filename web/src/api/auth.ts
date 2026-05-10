import { api } from './client'
import type { AuthResponse } from '@/types'

export async function signup(email: string, password: string, name: string): Promise<AuthResponse> {
  const { data } = await api.post<AuthResponse>('/api/v1/auth/signup', { email, password, name })
  return data
}

export async function login(email: string, password: string): Promise<AuthResponse> {
  const { data } = await api.post<AuthResponse>('/api/v1/auth/login', { email, password })
  return data
}

export async function refresh(): Promise<{ access_token: string }> {
  const { data } = await api.post<{ access_token: string }>('/api/v1/auth/refresh')
  return data
}

export async function logout(): Promise<void> {
  await api.post('/api/v1/auth/logout')
}
