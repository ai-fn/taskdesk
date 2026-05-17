import { api, getToken } from './client'
import type { DailyPlan, GetLatestPlanResponse } from '@/types'

export async function getLatestPlan(): Promise<GetLatestPlanResponse> {
  const { data } = await api.get<GetLatestPlanResponse>('/api/v1/plan')
  return data
}

// generatePlanSSE streams the generate endpoint via SSE so the UI gets a
// heartbeat immediately and the plan when Claude finishes.
// Resolves with the plan on success, rejects on error, resolves null on cancel.
export async function generatePlanSSE(signal: AbortSignal): Promise<DailyPlan | null> {
  const token = getToken()
  const response = await fetch('/api/v1/plan/generate', {
    method: 'POST',
    headers: token ? { Authorization: `Bearer ${token}` } : {},
    signal,
  })

  if (!response.ok) {
    throw new Error(`HTTP ${response.status}`)
  }

  const reader = response.body!.getReader()
  const decoder = new TextDecoder()
  let buffer = ''

  while (true) {
    const { done, value } = await reader.read()
    if (done) break

    buffer += decoder.decode(value, { stream: true })
    // SSE frames end with \n\n; split on that to get complete events.
    const frames = buffer.split('\n\n')
    buffer = frames.pop() ?? ''

    for (const frame of frames) {
      const line = frame.trim()
      if (!line.startsWith('data: ')) continue
      const event = JSON.parse(line.slice(6)) as {
        status: 'generating' | 'done' | 'error'
        plan?: DailyPlan
        error?: string
      }
      if (event.status === 'done' && event.plan) return event.plan
      if (event.status === 'error') throw new Error(event.error ?? 'generation failed')
      // 'generating' status → keep reading
    }
  }

  return null
}
