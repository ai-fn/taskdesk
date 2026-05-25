import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import type { Task } from '@/types'
import { Priority, Status, PriorityLabel } from '@/types'
import { Button } from '@/components/ui/Button'
import { useDeleteTask, useUpdateStatus, useToggleFlagged, useRestoreTask, usePurgeTask } from '@/hooks/useTaskMutations'
import { SubtaskList } from './SubtaskList'

const priorityColors: Record<Priority, string> = {
  [Priority.UNSPECIFIED]: 'bg-gray-100 text-gray-600 dark:bg-gray-700 dark:text-gray-300',
  [Priority.LOW]: 'bg-blue-100 text-blue-700 dark:bg-blue-900/40 dark:text-blue-300',
  [Priority.MEDIUM]: 'bg-yellow-100 text-yellow-700 dark:bg-yellow-900/40 dark:text-yellow-300',
  [Priority.HIGH]: 'bg-orange-100 text-orange-700 dark:bg-orange-900/40 dark:text-orange-300',
  [Priority.URGENT]: 'bg-red-100 text-red-700 dark:bg-red-900/40 dark:text-red-300',
}

function scoreColor(score: number) {
  if (score >= 70) return 'bg-green-100 text-green-700 dark:bg-green-900/40 dark:text-green-300'
  if (score >= 40) return 'bg-yellow-100 text-yellow-700 dark:bg-yellow-900/40 dark:text-yellow-300'
  return 'bg-red-100 text-red-700 dark:bg-red-900/40 dark:text-red-300'
}

const statusSelectColors: Record<Status, string> = {
  [Status.UNSPECIFIED]: 'bg-gray-100 text-gray-600 dark:bg-gray-700 dark:text-gray-300',
  [Status.TODO]: 'bg-slate-100 text-slate-700 dark:bg-slate-700 dark:text-slate-200',
  [Status.IN_PROGRESS]: 'bg-purple-100 text-purple-700 dark:bg-purple-900/40 dark:text-purple-300',
  [Status.DONE]: 'bg-green-100 text-green-700 dark:bg-green-900/40 dark:text-green-300',
}

interface Props {
  task: Task
  onEdit: (task: Task) => void
  filterKey?: string
  isDeletedView?: boolean
}

export function TaskCard({ task, onEdit, filterKey, isDeletedView }: Props) {
  const navigate = useNavigate()
  const del = useDeleteTask(filterKey)
  const updateStatus = useUpdateStatus(filterKey)
  const toggleFlagged = useToggleFlagged(filterKey)
  const restore = useRestoreTask(filterKey)
  const purge = usePurgeTask(filterKey)

  const [subtasksOpen, setSubtasksOpen] = useState(false)

  const isDone = task.status === Status.DONE
  const isTemp = task.id.startsWith('temp-')
  const isStatusPending = updateStatus.isPending && updateStatus.variables?.id === task.id
  const subtaskCount = task.subtask_count ?? 0
  const subtaskDone = task.subtask_done_count ?? 0
  const subtaskPct = subtaskCount > 0 ? Math.round((subtaskDone / subtaskCount) * 100) : 0

  function openDetail() {
    if (isTemp || isDeletedView) return
    navigate(`/tasks/detail/${task.id}`)
  }

  // Stop click propagation on interactive elements so they don't trigger the
  // card's navigation. Use onMouseDown too because some elements (selects)
  // open menus on mousedown before click fires.
  const stop = (e: React.SyntheticEvent) => e.stopPropagation()

  return (
    <div
      onClick={openDetail}
      role={isTemp || isDeletedView ? undefined : 'button'}
      tabIndex={isTemp || isDeletedView ? undefined : 0}
      onKeyDown={(e) => { if (e.key === 'Enter') openDetail() }}
      className={`rounded-lg border border-gray-200 dark:border-gray-700 bg-white dark:bg-gray-800 p-4 shadow-sm transition-opacity ${isDone ? 'opacity-60' : ''} ${!isTemp && !isDeletedView ? 'cursor-pointer hover:border-indigo-300 dark:hover:border-indigo-600' : ''}`}
    >
      <div className="flex items-start justify-between gap-3">
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-2">
            <p className={`font-medium text-gray-900 dark:text-gray-100 ${isDone ? 'line-through' : ''}`}>
              {task.title}
            </p>
            {task.flagged && <span className="text-red-500 text-sm">⚑</span>}
            {task.blocked && (
              <span
                title="Blocked by unfinished dependencies"
                className="text-amber-500 dark:text-amber-400 text-sm"
                aria-label="Blocked"
              >
                🔒
              </span>
            )}
          </div>
          {task.description && (
            <p className="mt-1 text-sm text-gray-500 dark:text-gray-400 line-clamp-2">{task.description}</p>
          )}
          <div className="mt-2 flex flex-wrap items-center gap-2">
            {task.priority !== Priority.UNSPECIFIED && (
              <span className={`rounded-full px-2 py-0.5 text-xs font-medium ${priorityColors[task.priority]}`}>
                {PriorityLabel[task.priority]}
              </span>
            )}
            {isTemp ? (
              <span className={`rounded-full px-2 py-0.5 text-xs font-medium ${statusSelectColors[task.status]}`}>
                To Do
              </span>
            ) : (
              <select
                value={task.status}
                disabled={isStatusPending || isDeletedView}
                onClick={stop}
                onMouseDown={stop}
                onChange={(e) => {
                  e.stopPropagation()
                  updateStatus.mutate({ id: task.id, status: Number(e.target.value) as Status })
                }}
                title={task.blocked ? 'Complete blocking tasks before marking this Done' : undefined}
                className={`rounded-full px-2 py-0.5 text-xs font-medium cursor-pointer border-0 outline-none focus:ring-1 focus:ring-indigo-400 disabled:opacity-50 ${statusSelectColors[task.status]}`}
              >
                <option value={Status.TODO}>To Do</option>
                <option value={Status.IN_PROGRESS}>In Progress</option>
                <option value={Status.DONE} disabled={task.blocked}>
                  Done{task.blocked ? ' (blocked)' : ''}
                </option>
              </select>
            )}
            {task.tags?.map((tag) => (
              <span key={tag} className="rounded-full bg-indigo-50 dark:bg-indigo-900/40 px-2 py-0.5 text-xs text-indigo-600 dark:text-indigo-300">
                {tag}
              </span>
            ))}
            {task.due_at && (
              <span className="text-xs text-gray-400 dark:text-gray-500">
                Due {new Date(task.due_at).toLocaleDateString()}
              </span>
            )}
            {task.completion_score != null && task.completion_score > 0 && (
              <span
                className={`rounded-full px-2 py-0.5 text-xs font-medium cursor-default ${scoreColor(task.completion_score)}`}
                title={task.completion_insight || undefined}
              >
                ✦ {task.completion_score}%
              </span>
            )}
          </div>

          {/* Subtask progress bar — shown only when subtasks exist */}
          {!isTemp && subtaskCount > 0 && (
            <button
              onClick={(e) => { e.stopPropagation(); setSubtasksOpen((o) => !o) }}
              className="mt-2 flex items-center gap-2 text-xs text-gray-500 dark:text-gray-400 hover:text-gray-700 dark:hover:text-gray-200 transition-colors"
            >
              <svg
                viewBox="0 0 16 16"
                fill="none"
                stroke="currentColor"
                strokeWidth="2"
                className={`w-3 h-3 shrink-0 transition-transform ${subtasksOpen ? 'rotate-90' : ''}`}
              >
                <path d="M6 4l4 4-4 4"/>
              </svg>
              <span>{subtaskDone}/{subtaskCount} subtasks done</span>
              <div className="w-16 h-1 rounded-full bg-gray-100 dark:bg-gray-700 overflow-hidden">
                <div
                  className="h-full rounded-full bg-indigo-400 dark:bg-indigo-500 transition-all"
                  style={{ width: `${subtaskPct}%` }}
                />
              </div>
            </button>
          )}

          {/* Subtask list — also opens via "+" button when count is 0 */}
          {!isTemp && subtasksOpen && (
            <div onClick={stop}>
              <SubtaskList parentTask={task} />
            </div>
          )}
        </div>

        {!isTemp && (
          <div className="flex shrink-0 gap-1" onClick={stop}>
            {isDeletedView ? (
              <>
                <Button
                  variant="ghost"
                  className="text-xs text-green-600 hover:text-green-800 dark:text-green-400 dark:hover:text-green-300"
                  loading={restore.isPending && restore.variables === task.id}
                  onClick={() => restore.mutate(task.id)}
                >
                  Restore
                </Button>
                <Button
                  variant="ghost"
                  className="text-xs text-red-500 hover:text-red-700 dark:text-red-400 dark:hover:text-red-300"
                  loading={purge.isPending && purge.variables === task.id}
                  onClick={() => {
                    if (confirm('Permanently delete this task? This cannot be undone.')) {
                      purge.mutate(task.id)
                    }
                  }}
                >
                  Delete
                </Button>
              </>
            ) : (
              <>
                <button
                  onClick={() => toggleFlagged.mutate({ id: task.id, flagged: !task.flagged })}
                  className={`rounded p-1 text-sm transition-colors ${
                    task.flagged
                      ? 'text-red-500 hover:text-red-700 dark:text-red-400 dark:hover:text-red-300'
                      : 'text-gray-300 dark:text-gray-600 hover:text-red-400 dark:hover:text-red-500'
                  }`}
                  title={task.flagged ? 'Unflag' : 'Flag'}
                >
                  ⚑
                </button>
                <button
                  onClick={() => setSubtasksOpen((o) => !o)}
                  className="rounded p-1 text-xs text-gray-300 dark:text-gray-600 hover:text-indigo-500 dark:hover:text-indigo-400 transition-colors"
                  title="Subtasks"
                >
                  ⊕
                </button>
                <Button
                  variant="ghost"
                  className="text-xs"
                  onClick={() => onEdit(task)}
                >
                  Edit
                </Button>
                <Button
                  variant="ghost"
                  className="text-xs text-red-500 hover:text-red-700 dark:text-red-400 dark:hover:text-red-300"
                  loading={del.isPending && del.variables === task.id}
                  onClick={() => del.mutate(task.id)}
                >
                  Del
                </Button>
              </>
            )}
          </div>
        )}
      </div>
    </div>
  )
}
