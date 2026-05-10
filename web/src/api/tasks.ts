import { api } from './client'
import type { Task, TaskListResponse } from '@/types'

export async function listTasks(pageToken?: string): Promise<TaskListResponse> {
  const { data } = await api.get<TaskListResponse>('/api/v1/tasks', {
    params: pageToken ? { page_token: pageToken } : undefined,
  })
  return data
}

export interface CreateTaskInput {
  title: string
  description?: string
  priority?: number
  tags?: string[]
  due_at?: string
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
}

export async function updateTask(id: string, input: UpdateTaskInput): Promise<Task> {
  const { data } = await api.patch<Task>(`/api/v1/tasks/${id}`, input)
  return data
}

export async function deleteTask(id: string): Promise<void> {
  await api.delete(`/api/v1/tasks/${id}`)
}

export async function completeTask(id: string): Promise<Task> {
  const { data } = await api.post<Task>(`/api/v1/tasks/${id}/complete`)
  return data
}
