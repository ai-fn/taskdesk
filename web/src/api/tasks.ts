import { api } from './client'
import type { Task, TaskListResponse } from '@/types'

export interface ListTasksFilter {
  pageToken?: string
  status?: number
  list_id?: string
  flagged_only?: boolean
  include_deleted?: boolean
  due_today?: boolean
  has_due_date?: boolean
  parent_task_id?: string
}

export async function listTasks(filter?: ListTasksFilter): Promise<TaskListResponse> {
  const params: Record<string, string> = {}
  if (filter?.pageToken) params.page_token = filter.pageToken
  if (filter?.status != null) params.status = String(filter.status)
  if (filter?.list_id) params.list_id = filter.list_id
  if (filter?.flagged_only) params.flagged_only = 'true'
  if (filter?.include_deleted) params.include_deleted = 'true'
  if (filter?.due_today) params.due_today = 'true'
  if (filter?.has_due_date) params.has_due_date = 'true'
  if (filter?.parent_task_id) params.parent_task_id = filter.parent_task_id
  const { data } = await api.get<TaskListResponse>('/api/v1/tasks', { params })
  return data
}

export interface CreateTaskInput {
  title: string
  description?: string
  priority?: number
  tags?: string[]
  due_at?: string
  list_id?: string
  flagged?: boolean
  parent_task_id?: string
}

export async function createTask(input: CreateTaskInput): Promise<Task> {
  const { data } = await api.post<Task>('/api/v1/tasks', input)
  return data
}

export async function getTask(id: string): Promise<Task> {
  const { data } = await api.get<Task>(`/api/v1/tasks/${id}`)
  return data
}

export interface UpdateTaskInput {
  title?: string
  description?: string
  priority?: number
  status?: number
  tags?: string[]
  due_at?: string
  list_id?: string | null  // null = remove from list
  flagged?: boolean
}

export async function updateTask(id: string, input: UpdateTaskInput): Promise<Task> {
  // Convert null list_id to empty string for the backend's pointer-based check
  const body: Record<string, unknown> = { ...input }
  if (input.list_id === null) body.list_id = ''
  const { data } = await api.patch<Task>(`/api/v1/tasks/${id}`, body)
  return data
}

export async function deleteTask(id: string): Promise<void> {
  await api.delete(`/api/v1/tasks/${id}`)
}

export async function completeTask(id: string): Promise<Task> {
  const { data } = await api.post<Task>(`/api/v1/tasks/${id}/complete`)
  return data
}

export async function restoreTask(id: string): Promise<Task> {
  const { data } = await api.post<Task>(`/api/v1/tasks/${id}/restore`)
  return data
}

export async function purgeTask(id: string): Promise<void> {
  await api.delete(`/api/v1/tasks/${id}/purge`)
}

export async function searchTasks(q: string): Promise<TaskListResponse> {
  const { data } = await api.get<TaskListResponse>('/api/v1/tasks/search', { params: { q } })
  return data
}

export interface ParsedTask {
  title: string
  description?: string
  priority?: number
  due_at?: string        // RFC3339 string
  list_name?: string     // best-guess name; caller resolves to list_id
  tags?: string[]
  flagged?: boolean
  subtasks?: string[]
}

export async function parseTask(text: string, listNames: string[], signal?: AbortSignal): Promise<ParsedTask> {
  const { data } = await api.post<ParsedTask>('/api/v1/tasks/parse', {
    text,
    list_names: listNames,
  }, { signal })
  return data
}
