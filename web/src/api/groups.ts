import { api as apiClient } from './client'
import type { ListGroup } from '@/types'

interface ListGroupsResponse {
  groups: ListGroup[]
}

export function listGroups(): Promise<ListGroup[]> {
  return apiClient.get<ListGroupsResponse>('/api/v1/groups').then((r) => r.data.groups ?? [])
}

export function createGroup(name: string): Promise<ListGroup> {
  return apiClient.post<ListGroup>('/api/v1/groups', { name }).then((r) => r.data)
}

export function updateGroup(id: string, name: string): Promise<ListGroup> {
  return apiClient.patch<ListGroup>(`/api/v1/groups/${id}`, { name }).then((r) => r.data)
}

export function deleteGroup(id: string): Promise<void> {
  return apiClient.delete(`/api/v1/groups/${id}`).then(() => undefined)
}

export function reorderGroups(groupIds: string[]): Promise<void> {
  return apiClient.post('/api/v1/groups/reorder', { group_ids: groupIds }).then(() => undefined)
}
