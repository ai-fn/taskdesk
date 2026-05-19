import { api, getToken } from './client'
import type { DailyPlan, GetLatestPlanResponse } from '@/types'

export async function getLatestPlan(): Promise<GetLatestPlanResponse> {
  const { data } = await api.get<GetLatestPlanResponse>('/api/v1/plan')
  return data
}

// generatePlanSSE streams the generate endpoint via SSE.
// onText is called for each word-by-word text delta from Claude.
// onDone is called immediately when the done event arrives (before stream close).
// Resolves with the plan on success, rejects on error (including when the
// stream ends without a terminal done/error event — that's a server bug
// surface, not a silent no-op).
export async function generatePlanSSE(
  signal: AbortSignal,
  onText: (delta: string) => void,
  onDone?: (plan: DailyPlan) => void,
): Promise<DailyPlan> {
  const token = getToken()

  // Wait up to 120 seconds for Claude to generate + return plan.
  // This is longer than server timeout (90s) to account for network latency.
  const timeoutController = new AbortController()
  const timeoutId = setTimeout(() => timeoutController.abort(), 120_000)

  // Combine user abort signal with timeout signal.
  const onUserAbort = () => timeoutController.abort()
  signal.addEventListener('abort', onUserAbort)

  try {
    const response = await fetch('/api/v1/plan/generate', {
      method: 'POST',
      headers: token ? { Authorization: `Bearer ${token}` } : {},
      signal: timeoutController.signal,
    })

    if (!response.ok) {
      throw new Error(`HTTP ${response.status}`)
    }

    const reader = response.body!.getReader()
    const decoder = new TextDecoder()
    let buffer = ''
    let receivedTerminal = false

    // Keep reading until we get a terminal event (done or error).
    // Don't stop early even if the stream seems quiet.
    while (!receivedTerminal) {
      const { done, value } = await reader.read()

      if (value) {
        buffer += decoder.decode(value, { stream: true })
      }

      // Process all complete frames in the buffer.
      const frames = buffer.split('\n\n')
      buffer = frames.pop() ?? ''

      for (const frame of frames) {
        const line = frame.trim()
        if (!line.startsWith('data: ')) continue
        let event: { type?: string; delta?: string; plan?: DailyPlan; error?: string }
        try {
          event = JSON.parse(line.slice(6))
        } catch {
          continue
        }
        if (event.type === 'text' && event.delta) {
          onText(event.delta)
        } else if (event.type === 'done') {
          receivedTerminal = true
          if (!event.plan) throw new Error('done event missing plan')
          onDone?.(event.plan)
          return event.plan
        } else if (event.type === 'error') {
          receivedTerminal = true
          throw new Error(event.error ?? 'generation failed')
        }
      }

      // If stream closed and we haven't received terminal event, that's an error.
      if (done && !receivedTerminal) {
        throw new Error('stream ended without a plan')
      }
    }
  } finally {
    clearTimeout(timeoutId)
    signal.removeEventListener('abort', onUserAbort)
  }

  // Should never reach here, but just in case.
  throw new Error('generation ended unexpectedly')
}
