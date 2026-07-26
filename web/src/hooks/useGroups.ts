import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { listGroups, createGroup, updateGroup, deleteGroup, reorderGroups } from '@/api/groups'
import { groupKeys } from '@/api/queryKeys'
import type { ListGroup } from '@/types'

export function useGroups() {
  return useQuery({
    queryKey: groupKeys.list(),
    queryFn: listGroups,
    staleTime: 60_000,
  })
}

export function useCreateGroup() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (name: string) => createGroup(name),
    onMutate: async (name) => {
      await qc.cancelQueries({ queryKey: groupKeys.list() })
      const snapshot = qc.getQueryData<ListGroup[]>(groupKeys.list())
      const optimistic: ListGroup = {
        id: 'temp-' + Date.now(),
        user_id: '',
        name,
        position: (snapshot?.length ?? 0),
        created_at: new Date().toISOString(),
        updated_at: new Date().toISOString(),
      }
      qc.setQueryData<ListGroup[]>(groupKeys.list(), (old) => [...(old ?? []), optimistic])
      return { snapshot }
    },
    onError: (_e, _v, ctx) => qc.setQueryData(groupKeys.list(), ctx?.snapshot),
    onSettled: () => qc.invalidateQueries({ queryKey: groupKeys.list() }),
  })
}

export function useUpdateGroup() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, name }: { id: string; name: string }) => updateGroup(id, name),
    onMutate: async ({ id, name }) => {
      await qc.cancelQueries({ queryKey: groupKeys.list() })
      const snapshot = qc.getQueryData<ListGroup[]>(groupKeys.list())
      qc.setQueryData<ListGroup[]>(groupKeys.list(), (old) =>
        old?.map((g) => (g.id === id ? { ...g, name } : g)) ?? []
      )
      return { snapshot }
    },
    onError: (_e, _v, ctx) => qc.setQueryData(groupKeys.list(), ctx?.snapshot),
    onSettled: () => qc.invalidateQueries({ queryKey: groupKeys.list() }),
  })
}

export function useDeleteGroup() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => deleteGroup(id),
    onMutate: async (id) => {
      await qc.cancelQueries({ queryKey: groupKeys.list() })
      const snapshot = qc.getQueryData<ListGroup[]>(groupKeys.list())
      qc.setQueryData<ListGroup[]>(groupKeys.list(), (old) => old?.filter((g) => g.id !== id) ?? [])
      return { snapshot }
    },
    onError: (_e, _v, ctx) => qc.setQueryData(groupKeys.list(), ctx?.snapshot),
    onSettled: () => qc.invalidateQueries({ queryKey: groupKeys.list() }),
  })
}

export function useReorderGroups() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (ids: string[]) => reorderGroups(ids),
    onMutate: async (ids) => {
      await qc.cancelQueries({ queryKey: groupKeys.list() })
      const snapshot = qc.getQueryData<ListGroup[]>(groupKeys.list())
      qc.setQueryData<ListGroup[]>(groupKeys.list(), (old) => {
        if (!old) return old
        const byId = new Map(old.map((g) => [g.id, g]))
        return ids.map((id, i) => ({ ...byId.get(id)!, position: i })).filter(Boolean)
      })
      return { snapshot }
    },
    onError: (_e, _v, ctx) => qc.setQueryData(groupKeys.list(), ctx?.snapshot),
    onSettled: () => qc.invalidateQueries({ queryKey: groupKeys.list() }),
  })
}
