import { useQuery } from '@tanstack/react-query'
import { listTasks } from '@/api/tasks'
import type { ListTasksFilter } from '@/api/tasks'
import { taskKeys } from '@/api/queryKeys'

export function useTasks(filter?: ListTasksFilter) {
  const filterKey = JSON.stringify(filter ?? {})
  return useQuery({
    queryKey: taskKeys.list(filterKey),
    queryFn: () => listTasks(filter),
    staleTime: 30_000,
  })
}
