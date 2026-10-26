import { api } from './client'
import type { Task, TaskListResponse } from '@/types'

export interface ListTasksFilter {
  pageToken?: string
  page_size?: number
  status?: number
  list_id?: string
  flagged_only?: boolean
  include_deleted?: boolean
  due_today?: boolean
  has_due_date?: boolean
  parent_task_id?: string
  exclude_blocked?: boolean
}

export async function listTasks(filter?: ListTasksFilter, signal?: AbortSignal): Promise<TaskListResponse> {
  const params: Record<string, string> = {}
  if (filter?.pageToken) params.page_token = filter.pageToken
  if (filter?.page_size) params.page_size = String(filter.page_size)
  if (filter?.status != null) params.status = String(filter.status)
  if (filter?.list_id) params.list_id = filter.list_id
  if (filter?.flagged_only) params.flagged_only = 'true'
  if (filter?.include_deleted) params.include_deleted = 'true'
  if (filter?.due_today) params.due_today = 'true'
  if (filter?.has_due_date) params.has_due_date = 'true'
  if (filter?.parent_task_id) params.parent_task_id = filter.parent_task_id
  if (filter?.exclude_blocked) params.exclude_blocked = 'true'
  const { data } = await api.get<TaskListResponse>('/api/v1/tasks', { params, signal })
  return data
}

const PAGE_SIZE = 200 // the server maximum
const MAX_PAGES = 10 // 2000 tasks; past that the result is returned truncated

/**
 * Fetches every page of a listing, so views never silently stop at the first page. Keeps the flat
 * `{ tasks }` shape the optimistic-update cache code relies on. A non-empty `next_page_token`
 * in the result means the MAX_PAGES cap was hit (the server also emits a token after an exactly
 * full last page, so a list of exactly 2000 reads as truncated).
 */
export async function listAllTasks(filter?: ListTasksFilter, signal?: AbortSignal): Promise<TaskListResponse> {
  const tasks: Task[] = []
  let pageToken = filter?.pageToken
  for (let page = 0; page < MAX_PAGES; page++) {
    signal?.throwIfAborted() // a cancelled query (optimistic update) must not keep walking pages
    const res = await listTasks({ ...filter, pageToken, page_size: PAGE_SIZE }, signal)
    tasks.push(...res.tasks)
    if (!res.next_page_token) return { tasks, next_page_token: '' }
    pageToken = res.next_page_token
  }
  return { tasks, next_page_token: pageToken ?? '' }
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

export interface TaskDependencies {
  depends_on: Task[]
  blocking: Task[]
}

export async function listDependencies(taskId: string): Promise<TaskDependencies> {
  const { data } = await api.get<TaskDependencies>(`/api/v1/tasks/${taskId}/dependencies`)
  return { depends_on: data.depends_on ?? [], blocking: data.blocking ?? [] }
}

export async function addDependency(taskId: string, dependsOnId: string): Promise<Task> {
  const { data } = await api.post<Task>(`/api/v1/tasks/${taskId}/dependencies`, {
    depends_on_id: dependsOnId,
  })
  return data
}

export async function removeDependency(taskId: string, dependsOnId: string): Promise<void> {
  await api.delete(`/api/v1/tasks/${taskId}/dependencies/${dependsOnId}`)
}

export interface DependencyTreeNode {
  task: Task
  depth: number
  via_task_id: string
}

export interface DependencyTree {
  root: Task
  depends_on: DependencyTreeNode[]
  blocking: DependencyTreeNode[]
}

export async function getDependencyTree(taskId: string): Promise<DependencyTree> {
  const { data } = await api.get<DependencyTree>(`/api/v1/tasks/${taskId}/dependency-tree`)
  return {
    root: data.root,
    depends_on: data.depends_on ?? [],
    blocking: data.blocking ?? [],
  }
}
