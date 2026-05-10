import { useQuery } from '@tanstack/react-query'
import { listTasks } from '@/api/tasks'
import { taskKeys } from '@/api/queryKeys'

export function useTasks(pageToken?: string) {
  return useQuery({
    queryKey: taskKeys.list(),
    queryFn: () => listTasks(pageToken),
    staleTime: 30_000,
  })
}
