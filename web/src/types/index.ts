export const Priority = {
  UNSPECIFIED: 0,
  LOW: 1,
  MEDIUM: 2,
  HIGH: 3,
  URGENT: 4,
} as const
export type Priority = (typeof Priority)[keyof typeof Priority]

export const Status = {
  UNSPECIFIED: 0,
  TODO: 1,
  IN_PROGRESS: 2,
  DONE: 3,
} as const
export type Status = (typeof Status)[keyof typeof Status]

export interface Task {
  id: string
  user_id: string
  title: string
  description: string
  priority: Priority
  status: Status
  tags?: string[]
  due_at?: string       // RFC-3339 or absent when nil
  created_at: string
  updated_at: string
}

export interface TaskListResponse {
  tasks: Task[]
  next_page_token: string
}

export interface AuthResponse {
  user_id: string
  access_token: string
}

export interface ApiError {
  error: string
}

export const PriorityLabel: Record<Priority, string> = {
  [Priority.UNSPECIFIED]: 'None',
  [Priority.LOW]: 'Low',
  [Priority.MEDIUM]: 'Medium',
  [Priority.HIGH]: 'High',
  [Priority.URGENT]: 'Urgent',
}

export const StatusLabel: Record<Status, string> = {
  [Status.UNSPECIFIED]: 'Unknown',
  [Status.TODO]: 'To Do',
  [Status.IN_PROGRESS]: 'In Progress',
  [Status.DONE]: 'Done',
}
