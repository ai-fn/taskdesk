import { useQuery } from '@tanstack/react-query'
import { listAllTasks } from '@/api/tasks'
import type { ListTasksFilter } from '@/api/tasks'
import { taskKeys } from '@/api/queryKeys'

export function useTasks(filter?: ListTasksFilter) {
  const filterKey = JSON.stringify(filter ?? {})
  return useQuery({
    queryKey: taskKeys.list(filterKey),
    queryFn: ({ signal }) => listAllTasks(filter, signal),
    staleTime: 30_000,
  })
}
