import { useMutation, useQueryClient } from '@tanstack/react-query'
import { createTask, updateTask, deleteTask, completeTask, restoreTask, purgeTask } from '@/api/tasks'
import type { CreateTaskInput, UpdateTaskInput } from '@/api/tasks'
import { taskKeys } from '@/api/queryKeys'
import type { Task, TaskListResponse } from '@/types'
import { Status } from '@/types'

function useInvalidateAll() {
  const qc = useQueryClient()
  return () => qc.invalidateQueries({ queryKey: taskKeys.all })
}

export function useCreateTask() {
  const qc = useQueryClient()
  const invalidate = useInvalidateAll()

  return useMutation({
    mutationFn: (input: CreateTaskInput) => createTask(input),
    onMutate: async (input) => {
      await qc.cancelQueries({ queryKey: taskKeys.all })
      const filterKey = JSON.stringify({})
      const key = taskKeys.list(filterKey)
      const snapshot = qc.getQueryData<TaskListResponse>(key)
      qc.setQueryData<TaskListResponse>(key, (old) => {
        const optimistic: Task = {
          id: 'temp-' + Date.now(),
          user_id: '',
          title: input.title,
          description: input.description ?? '',
          priority: (input.priority ?? 0) as Task['priority'],
          status: Status.TODO,
          tags: input.tags,
          due_at: input.due_at,
          list_id: input.list_id,
          flagged: input.flagged,
          created_at: new Date().toISOString(),
          updated_at: new Date().toISOString(),
        }
        return { tasks: [optimistic, ...(old?.tasks ?? [])], next_page_token: old?.next_page_token ?? '' }
      })
      return { snapshot, key }
    },
    onError: (_err, _vars, ctx) => {
      if (ctx?.snapshot) qc.setQueryData(ctx.key, ctx.snapshot)
    },
    onSettled: invalidate,
  })
}

export function useUpdateTask() {
  const invalidate = useInvalidateAll()
  return useMutation({
    mutationFn: ({ id, input }: { id: string; input: UpdateTaskInput }) => updateTask(id, input),
    onSettled: invalidate,
  })
}

export function useDeleteTask(filterKey?: string) {
  const qc = useQueryClient()
  const invalidate = useInvalidateAll()

  return useMutation({
    mutationFn: (id: string) => deleteTask(id),
    onMutate: async (id) => {
      await qc.cancelQueries({ queryKey: taskKeys.all })
      const key = taskKeys.list(filterKey ?? JSON.stringify({}))
      const snapshot = qc.getQueryData<TaskListResponse>(key)
      qc.setQueryData<TaskListResponse>(key, (old) => ({
        tasks: (old?.tasks ?? []).filter((t) => t.id !== id),
        next_page_token: old?.next_page_token ?? '',
      }))
      return { snapshot, key }
    },
    onError: (_err, _vars, ctx) => {
      if (ctx?.snapshot) qc.setQueryData(ctx.key, ctx.snapshot)
    },
    onSettled: invalidate,
  })
}

export function useUpdateStatus(filterKey?: string) {
  const qc = useQueryClient()
  const invalidate = useInvalidateAll()

  return useMutation({
    mutationFn: ({ id, status }: { id: string; status: Status }) =>
      updateTask(id, { status }),
    onMutate: async ({ id, status }) => {
      await qc.cancelQueries({ queryKey: taskKeys.all })
      const key = taskKeys.list(filterKey ?? JSON.stringify({}))
      const snapshot = qc.getQueryData<TaskListResponse>(key)
      qc.setQueryData<TaskListResponse>(key, (old) => ({
        tasks: (old?.tasks ?? []).map((t) => t.id === id ? { ...t, status } : t),
        next_page_token: old?.next_page_token ?? '',
      }))
      return { snapshot, key }
    },
    onError: (_err, _vars, ctx) => {
      if (ctx?.snapshot) qc.setQueryData(ctx.key, ctx.snapshot)
    },
    onSettled: invalidate,
  })
}

export function useCompleteTask(filterKey?: string) {
  const qc = useQueryClient()
  const invalidate = useInvalidateAll()

  return useMutation({
    mutationFn: (id: string) => completeTask(id),
    onMutate: async (id) => {
      await qc.cancelQueries({ queryKey: taskKeys.all })
      const key = taskKeys.list(filterKey ?? JSON.stringify({}))
      const snapshot = qc.getQueryData<TaskListResponse>(key)
      qc.setQueryData<TaskListResponse>(key, (old) => ({
        tasks: (old?.tasks ?? []).map((t) => t.id === id ? { ...t, status: Status.DONE } : t),
        next_page_token: old?.next_page_token ?? '',
      }))
      return { snapshot, key }
    },
    onError: (_err, _vars, ctx) => {
      if (ctx?.snapshot) qc.setQueryData(ctx.key, ctx.snapshot)
    },
    onSettled: invalidate,
  })
}

export function useToggleFlagged(filterKey?: string) {
  const qc = useQueryClient()
  const invalidate = useInvalidateAll()

  return useMutation({
    mutationFn: ({ id, flagged }: { id: string; flagged: boolean }) =>
      updateTask(id, { flagged }),
    onMutate: async ({ id, flagged }) => {
      await qc.cancelQueries({ queryKey: taskKeys.all })
      const key = taskKeys.list(filterKey ?? JSON.stringify({}))
      const snapshot = qc.getQueryData<TaskListResponse>(key)
      qc.setQueryData<TaskListResponse>(key, (old) => ({
        tasks: (old?.tasks ?? []).map((t) => t.id === id ? { ...t, flagged } : t),
        next_page_token: old?.next_page_token ?? '',
      }))
      return { snapshot, key }
    },
    onError: (_err, _vars, ctx) => {
      if (ctx?.snapshot) qc.setQueryData(ctx.key, ctx.snapshot)
    },
    onSettled: invalidate,
  })
}

export function useRestoreTask(filterKey?: string) {
  const qc = useQueryClient()
  const invalidate = useInvalidateAll()

  return useMutation({
    mutationFn: (id: string) => restoreTask(id),
    onMutate: async (id) => {
      await qc.cancelQueries({ queryKey: taskKeys.all })
      const key = taskKeys.list(filterKey ?? JSON.stringify({}))
      const snapshot = qc.getQueryData<TaskListResponse>(key)
      qc.setQueryData<TaskListResponse>(key, (old) => ({
        tasks: (old?.tasks ?? []).filter((t) => t.id !== id),
        next_page_token: old?.next_page_token ?? '',
      }))
      return { snapshot, key }
    },
    onError: (_err, _vars, ctx) => {
      if (ctx?.snapshot) qc.setQueryData(ctx.key, ctx.snapshot)
    },
    onSettled: invalidate,
  })
}

export function usePurgeTask(filterKey?: string) {
  const qc = useQueryClient()
  const invalidate = useInvalidateAll()

  return useMutation({
    mutationFn: (id: string) => purgeTask(id),
    onMutate: async (id) => {
      await qc.cancelQueries({ queryKey: taskKeys.all })
      const key = taskKeys.list(filterKey ?? JSON.stringify({}))
      const snapshot = qc.getQueryData<TaskListResponse>(key)
      qc.setQueryData<TaskListResponse>(key, (old) => ({
        tasks: (old?.tasks ?? []).filter((t) => t.id !== id),
        next_page_token: old?.next_page_token ?? '',
      }))
      return { snapshot, key }
    },
    onError: (_err, _vars, ctx) => {
      if (ctx?.snapshot) qc.setQueryData(ctx.key, ctx.snapshot)
    },
    onSettled: invalidate,
  })
}
