import { api } from './client'

export interface SubscriptionInfo {
  plan: 'free' | 'pro'
  source: string
  is_active: boolean
}

export async function getSubscription(): Promise<SubscriptionInfo> {
  const { data } = await api.get<{ subscription: SubscriptionInfo }>('/api/v1/subscriptions')
  return data.subscription
}

export async function createCheckout(): Promise<{ checkout_url: string }> {
  const { data } = await api.post<{ checkout_url: string }>('/api/v1/subscriptions/checkout')
  return data
}

export async function redeemPromoCode(code: string): Promise<{ plan: string }> {
  const { data } = await api.post<{ plan: string }>('/api/v1/subscriptions/redeem', { code })
  return data
}
