import { api } from './client'

export interface Session {
  id: string
  browser: string
  os: string
  device: string
  ip_address: string
  created_at: string
  last_active_at: string
  current: boolean
}

export async function listSessions(): Promise<Session[]> {
  const { data } = await api.get<{ sessions: Session[] }>('/api/v1/sessions')
  return data.sessions ?? []
}

export async function revokeSession(id: string): Promise<void> {
  await api.delete(`/api/v1/sessions/${id}`)
}

export async function revokeOtherSessions(): Promise<number> {
  const { data } = await api.post<{ revoked: number }>('/api/v1/sessions/revoke-others')
  return data.revoked ?? 0
}
