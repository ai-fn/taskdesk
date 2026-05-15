import { api } from './client'
import type { GetLatestPlanResponse } from '@/types'

export async function getLatestPlan(): Promise<GetLatestPlanResponse> {
  const { data } = await api.get<GetLatestPlanResponse>('/api/v1/plan')
  return data
}
