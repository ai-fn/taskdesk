import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { listLists, createList, updateList, deleteList, reorderLists } from '@/api/lists'
import type { CreateListInput, UpdateListInput } from '@/api/lists'
import { listKeys } from '@/api/queryKeys'
import type { TaskList } from '@/types'

export function useLists() {
  return useQuery({
    queryKey: listKeys.list(),
    queryFn: listLists,
    staleTime: 30_000,
  })
}

export function useCreateList() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (input: CreateListInput) => createList(input),
    onMutate: async (input) => {
      await qc.cancelQueries({ queryKey: listKeys.list() })
      const snapshot = qc.getQueryData<TaskList[]>(listKeys.list())
      const optimistic: TaskList = {
        id: 'temp-' + Date.now(),
        user_id: '',
        name: input.name,
        color: input.color,
        position: (snapshot?.length ?? 0),
        is_hidden: false,
        task_count: 0,
        created_at: new Date().toISOString(),
        updated_at: new Date().toISOString(),
      }
      qc.setQueryData<TaskList[]>(listKeys.list(), (old) => [...(old ?? []), optimistic])
      return { snapshot }
    },
    onError: (_err, _vars, ctx) => {
      if (ctx?.snapshot) qc.setQueryData(listKeys.list(), ctx.snapshot)
    },
    onSettled: () => qc.invalidateQueries({ queryKey: listKeys.list() }),
  })
}

export function useUpdateList() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, input }: { id: string; input: UpdateListInput }) => updateList(id, input),
    onMutate: async ({ id, input }) => {
      await qc.cancelQueries({ queryKey: listKeys.list() })
      const snapshot = qc.getQueryData<TaskList[]>(listKeys.list())
      qc.setQueryData<TaskList[]>(listKeys.list(), (old) =>
        (old ?? []).map((l) => {
          if (l.id !== id) return l
          const { group_id, ...rest } = input
          const patch: Partial<TaskList> = {
            ...rest,
            ...(group_id !== undefined ? { group_id: group_id ?? undefined } : {}),
          }
          return { ...l, ...patch }
        })
      )
      return { snapshot }
    },
    onError: (_err, _vars, ctx) => {
      if (ctx?.snapshot) qc.setQueryData(listKeys.list(), ctx.snapshot)
    },
    onSettled: () => qc.invalidateQueries({ queryKey: listKeys.list() }),
  })
}

export function useDeleteList() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => deleteList(id),
    onMutate: async (id) => {
      await qc.cancelQueries({ queryKey: listKeys.list() })
      const snapshot = qc.getQueryData<TaskList[]>(listKeys.list())
      qc.setQueryData<TaskList[]>(listKeys.list(), (old) =>
        (old ?? []).filter((l) => l.id !== id)
      )
      return { snapshot }
    },
    onError: (_err, _vars, ctx) => {
      if (ctx?.snapshot) qc.setQueryData(listKeys.list(), ctx.snapshot)
    },
    onSettled: () => qc.invalidateQueries({ queryKey: listKeys.list() }),
  })
}

export function useReorderLists() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (listIds: string[]) => reorderLists(listIds),
    onMutate: async (listIds) => {
      await qc.cancelQueries({ queryKey: listKeys.list() })
      const snapshot = qc.getQueryData<TaskList[]>(listKeys.list())
      qc.setQueryData<TaskList[]>(listKeys.list(), (old) => {
        if (!old) return old
        const sorted = listIds
          .map((id) => old.find((l) => l.id === id))
          .filter((l): l is TaskList => l != null)
        const rest = old.filter((l) => !listIds.includes(l.id))
        return [...sorted.map((l, i) => ({ ...l, position: i })), ...rest]
      })
      return { snapshot }
    },
    onError: (_err, _vars, ctx) => {
      if (ctx?.snapshot) qc.setQueryData(listKeys.list(), ctx.snapshot)
    },
    onSettled: () => qc.invalidateQueries({ queryKey: listKeys.list() }),
  })
}
