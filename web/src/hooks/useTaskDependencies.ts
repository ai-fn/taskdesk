import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { addDependency, listDependencies, removeDependency } from '@/api/tasks'
import { taskKeys } from '@/api/queryKeys'

export function useDependencies(taskId: string | undefined, enabled = true) {
  return useQuery({
    queryKey: taskKeys.dependencies(taskId ?? ''),
    queryFn: () => listDependencies(taskId!),
    enabled: !!taskId && enabled,
    staleTime: 30_000,
  })
}

export function useAddDependency(taskId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (dependsOnId: string) => addDependency(taskId, dependsOnId),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: taskKeys.dependencies(taskId) })
      qc.invalidateQueries({ queryKey: taskKeys.all })
    },
  })
}

export function useRemoveDependency(taskId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (dependsOnId: string) => removeDependency(taskId, dependsOnId),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: taskKeys.dependencies(taskId) })
      qc.invalidateQueries({ queryKey: taskKeys.all })
    },
  })
}
