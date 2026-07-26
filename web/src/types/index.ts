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
  due_at?: string     // RFC-3339, absent when nil
  created_at: string
  updated_at: string
  list_id?: string
  flagged?: boolean
  deleted_at?: string    // present when soft-deleted
  completion_score?: number    // 0–100; absent until scored
  completion_insight?: string  // Claude's one-liner
  parent_task_id?: string      // absent = root task
  subtask_count?: number       // computed
  subtask_done_count?: number  // computed
  blocked?: boolean              // computed: true if any dependency is unfinished
  depends_on_task_ids?: string[] // computed: task IDs this is blocked by
}

export interface TaskList {
  id: string
  user_id: string
  name: string
  color: string
  position: number
  is_hidden: boolean
  task_count: number
  group_id?: string
  icon?: string
  created_at: string
  updated_at: string
}

export interface ListGroup {
  id: string
  user_id: string
  name: string
  position: number
  created_at: string
  updated_at: string
}

export const SYSTEM_LISTS = {
  ALL:       { id: 'all',       nameKey: 'lists.system.all',       color: '#8E8E93', icon: '∞' },
  TODAY:     { id: 'today',     nameKey: 'lists.system.today',     color: '#FF9500', icon: '☀' },
  SCHEDULED: { id: 'scheduled', nameKey: 'lists.system.scheduled', color: '#007AFF', icon: '📅' },
  FLAGGED:   { id: 'flagged',   nameKey: 'lists.system.flagged',   color: '#FF3B30', icon: '⚑' },
  COMPLETED: { id: 'completed', nameKey: 'lists.system.completed', color: '#34C759', icon: '✓' },
  DELETED:   { id: 'deleted',   nameKey: 'lists.system.deleted',   color: '#8E8E93', icon: '🗑' },
} as const

export type SystemListId = typeof SYSTEM_LISTS[keyof typeof SYSTEM_LISTS]['id']

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

// PriorityLabelKey maps each priority to its i18n key.
// In components: const { t } = useTranslation(); t(PriorityLabelKey[task.priority])
export const PriorityLabelKey: Record<Priority, string> = {
  [Priority.UNSPECIFIED]: 'tasks.priority.none',
  [Priority.LOW]:         'tasks.priority.low',
  [Priority.MEDIUM]:      'tasks.priority.medium',
  [Priority.HIGH]:        'tasks.priority.high',
  [Priority.URGENT]:      'tasks.priority.urgent',
}

// StatusLabelKey maps each status to its i18n key.
export const StatusLabelKey: Record<Status, string> = {
  [Status.UNSPECIFIED]: 'tasks.status.unknown',
  [Status.TODO]:        'tasks.status.todo',
  [Status.IN_PROGRESS]: 'tasks.status.inProgress',
  [Status.DONE]:        'tasks.status.done',
}
