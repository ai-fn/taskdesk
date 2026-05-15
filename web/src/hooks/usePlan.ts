import { useQuery } from '@tanstack/react-query'
import { getLatestPlan } from '@/api/plan'
import { planKeys } from '@/api/queryKeys'

export function usePlan() {
  return useQuery({
    queryKey: planKeys.latest(),
    queryFn: getLatestPlan,
    staleTime: 5 * 60 * 1000, // plan changes at most once per day; 5 min stale is fine
  })
}
