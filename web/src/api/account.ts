import { api } from './client'

export interface Me {
  user_id: string
  email: string
  name: string
  language: string
  email_verified: boolean
}

export async function getMe(): Promise<Me> {
  const { data } = await api.get<Me>('/api/v1/account/me')
  return data
}

export async function updateLanguage(language: string): Promise<void> {
  await api.patch('/api/v1/account/language', { language })
}

export async function requestEmailVerification(): Promise<void> {
  await api.post('/api/v1/account/email/verify/request')
}

export async function confirmEmail(code: string): Promise<void> {
  await api.post('/api/v1/account/email/verify/confirm', { code })
}
