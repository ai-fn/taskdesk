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
  due_at?: string   // RFC-3339, absent when nil
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

export interface PlanItem {
  task_id: string
  task_title: string
  rank: number
  rationale: string
}

export interface DailyPlan {
  plan_id: string
  user_id: string
  ordered_task_ids: string[]
  items: PlanItem[]
  summary: string
  generated_at: string
}

export interface GetLatestPlanResponse {
  plan?: DailyPlan
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
