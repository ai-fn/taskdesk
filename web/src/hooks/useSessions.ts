import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { sessionKeys } from '@/api/queryKeys'
import { listSessions, revokeSession, revokeOtherSessions } from '@/api/sessions'
import type { Session } from '@/api/sessions'

export function useSessions() {
  return useQuery({
    queryKey: sessionKeys.list(),
    queryFn: listSessions,
    staleTime: 15_000,
  })
}

export function useRevokeSession() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => revokeSession(id),
    onMutate: async (id) => {
      await qc.cancelQueries({ queryKey: sessionKeys.list() })
      const snapshot = qc.getQueryData<Session[]>(sessionKeys.list())
      qc.setQueryData<Session[]>(sessionKeys.list(), (old) =>
        (old ?? []).filter((s) => s.id !== id),
      )
      return { snapshot }
    },
    onError: (_err, _vars, ctx) => {
      if (ctx?.snapshot) qc.setQueryData(sessionKeys.list(), ctx.snapshot)
    },
    onSettled: () => qc.invalidateQueries({ queryKey: sessionKeys.list() }),
  })
}

export function useRevokeOtherSessions() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () => revokeOtherSessions(),
    onMutate: async () => {
      await qc.cancelQueries({ queryKey: sessionKeys.list() })
      const snapshot = qc.getQueryData<Session[]>(sessionKeys.list())
      qc.setQueryData<Session[]>(sessionKeys.list(), (old) =>
        (old ?? []).filter((s) => s.current),
      )
      return { snapshot }
    },
    onError: (_err, _vars, ctx) => {
      if (ctx?.snapshot) qc.setQueryData(sessionKeys.list(), ctx.snapshot)
    },
    onSettled: () => qc.invalidateQueries({ queryKey: sessionKeys.list() }),
  })
}
