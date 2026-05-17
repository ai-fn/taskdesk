import { useRef, useState } from 'react'
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
  const [isError, setIsError] = useState(false)
  const abortRef = useRef<AbortController | null>(null)

  async function mutate() {
    const controller = new AbortController()
    abortRef.current = controller
    setIsPending(true)
    setIsError(false)

    try {
      const plan = await generatePlanSSE(controller.signal)
      if (plan) {
        queryClient.setQueryData(planKeys.latest(), { plan })
      }
    } catch (err) {
      if (!controller.signal.aborted) {
        setIsError(true)
      }
    } finally {
      setIsPending(false)
    }
  }

  function cancel() {
    abortRef.current?.abort()
    setIsPending(false)
    setIsError(false)
  }

  return { mutate, cancel, isPending, isError }
}
