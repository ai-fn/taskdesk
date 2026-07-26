import { useRef, useState } from 'react'
import { flushSync } from 'react-dom'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { generatePlanSSE, getLatestPlan } from '@/api/plan'
import { planKeys } from '@/api/queryKeys'

export function usePlan() {
  return useQuery({
    queryKey: planKeys.latest(),
    queryFn: getLatestPlan,
    staleTime: 5 * 60 * 1000,
  })
}

export function useGeneratePlan() {
  const queryClient = useQueryClient()
  const [isPending, setIsPending] = useState(false)
  const [errorMessage, setErrorMessage] = useState<string | null>(null)
  // deltas as an array (not a concatenated string) so each chunk can mount as
  // its own <span> and animate in independently.
  const [deltas, setDeltas] = useState<string[]>([])
  const abortRef = useRef<AbortController | null>(null)

  async function mutate() {
    const controller = new AbortController()
    abortRef.current = controller
    setIsPending(true)
    setErrorMessage(null)
    setDeltas([])
    // Accumulate the streamed narrative locally — this becomes the displayed
    // summary once 'done' lands, so the user keeps seeing the exact text that
    // streamed in (we ignore whatever summary the server tags on the plan).
    let streamed = ''

    try {
      await generatePlanSSE(
        controller.signal,
        (delta) => {
          streamed += delta
          // flushSync forces React to paint each delta immediately — otherwise
          // automatic batching collapses rapid setDeltas calls into one render
          // at the end, and the user sees no streaming.
          flushSync(() => setDeltas((prev) => [...prev, delta]))
        },
        (plan) => {
          // Called immediately when the done SSE event arrives — don't wait for stream close.
          queryClient.setQueryData(planKeys.latest(), { plan: { ...plan, summary: streamed } })
          setIsPending(false)
          setDeltas([])
        },
      )
    } catch (err) {
      if (!controller.signal.aborted) {
        let msg = err instanceof Error ? err.message : 'generation failed'
        // Strip raw gRPC prefix: "rpc error: code = X desc = <actual message>"
        const descMatch = msg.match(/desc\s*=\s*(.+)$/)
        if (descMatch) msg = descMatch[1].trim()
        setErrorMessage(msg)
      }
    } finally {
      setIsPending(false)
    }
  }

  function cancel() {
    abortRef.current?.abort()
    setIsPending(false)
    setErrorMessage(null)
    setDeltas([])
  }

  return { mutate, cancel, isPending, errorMessage, deltas }
}
