import { useMutation, useQueryClient } from '@tanstack/react-query'
import { createTask, updateTask, deleteTask, completeTask } from '@/api/tasks'
import type { CreateTaskInput, UpdateTaskInput } from '@/api/tasks'
import { taskKeys } from '@/api/queryKeys'
import type { Task, TaskListResponse } from '@/types'
import { Status } from '@/types'

function useInvalidateList() {
  const qc = useQueryClient()
  return () => qc.invalidateQueries({ queryKey: taskKeys.list() })
}

export function useCreateTask() {
  const qc = useQueryClient()
  const invalidate = useInvalidateList()

  return useMutation({
    mutationFn: (input: CreateTaskInput) => createTask(input),
    onMutate: async (input) => {
      await qc.cancelQueries({ queryKey: taskKeys.list() })
      const snapshot = qc.getQueryData<TaskListResponse>(taskKeys.list())
      qc.setQueryData<TaskListResponse>(taskKeys.list(), (old) => {
        const optimistic: Task = {
          id: 'temp-' + Date.now(),
          user_id: '',
          title: input.title,
          description: input.description ?? '',
          priority: (input.priority ?? 0) as Task['priority'],
          status: Status.TODO,
          tags: input.tags,
          due_at: input.due_at,
          created_at: new Date().toISOString(),
          updated_at: new Date().toISOString(),
        }
        return { tasks: [optimistic, ...(old?.tasks ?? [])], next_page_token: old?.next_page_token ?? '' }
      })
      return { snapshot }
    },
    onError: (_err, _vars, ctx) => {
      if (ctx?.snapshot) qc.setQueryData(taskKeys.list(), ctx.snapshot)
    },
    onSettled: invalidate,
  })
}

export function useUpdateTask() {
  const invalidate = useInvalidateList()
  return useMutation({
    mutationFn: ({ id, input }: { id: string; input: UpdateTaskInput }) => updateTask(id, input),
    onSettled: invalidate,
  })
}

export function useDeleteTask() {
  const qc = useQueryClient()
  const invalidate = useInvalidateList()

  return useMutation({
    mutationFn: (id: string) => deleteTask(id),
    onMutate: async (id) => {
      await qc.cancelQueries({ queryKey: taskKeys.list() })
      const snapshot = qc.getQueryData<TaskListResponse>(taskKeys.list())
      qc.setQueryData<TaskListResponse>(taskKeys.list(), (old) => ({
        tasks: (old?.tasks ?? []).filter((t) => t.id !== id),
        next_page_token: old?.next_page_token ?? '',
      }))
      return { snapshot }
    },
    onError: (_err, _vars, ctx) => {
      if (ctx?.snapshot) qc.setQueryData(taskKeys.list(), ctx.snapshot)
    },
    onSettled: invalidate,
  })
}

export function useCompleteTask() {
  const qc = useQueryClient()
  const invalidate = useInvalidateList()

  return useMutation({
    mutationFn: (id: string) => completeTask(id),
    onMutate: async (id) => {
      await qc.cancelQueries({ queryKey: taskKeys.list() })
      const snapshot = qc.getQueryData<TaskListResponse>(taskKeys.list())
      qc.setQueryData<TaskListResponse>(taskKeys.list(), (old) => ({
        tasks: (old?.tasks ?? []).map((t) =>
          t.id === id ? { ...t, status: Status.DONE } : t,
        ),
        next_page_token: old?.next_page_token ?? '',
      }))
      return { snapshot }
    },
    onError: (_err, _vars, ctx) => {
      if (ctx?.snapshot) qc.setQueryData(taskKeys.list(), ctx.snapshot)
    },
    onSettled: invalidate,
  })
}
