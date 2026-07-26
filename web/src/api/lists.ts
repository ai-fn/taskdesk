import { api } from './client'
import type { TaskList } from '@/types'

interface ListListsResponse {
  lists: TaskList[]
}

interface ReorderResponse {
  lists: TaskList[]
}

export async function listLists(): Promise<TaskList[]> {
  const { data } = await api.get<ListListsResponse>('/api/v1/lists')
  return data.lists ?? []
}

export interface CreateListInput {
  name: string
  color: string
  group_id?: string
  icon?: string
}

export async function createList(input: CreateListInput): Promise<TaskList> {
  const { data } = await api.post<TaskList>('/api/v1/lists', input)
  return data
}

export interface UpdateListInput {
  name?: string
  color?: string
  is_hidden?: boolean
  group_id?: string | null
  icon?: string
}

export async function updateList(id: string, input: UpdateListInput): Promise<TaskList> {
  const { data } = await api.patch<TaskList>(`/api/v1/lists/${id}`, input)
  return data
}

export async function deleteList(id: string): Promise<void> {
  await api.delete(`/api/v1/lists/${id}`)
}

export async function reorderLists(listIds: string[]): Promise<TaskList[]> {
  const { data } = await api.post<ReorderResponse>('/api/v1/lists/reorder', { list_ids: listIds })
  return data.lists ?? []
}
