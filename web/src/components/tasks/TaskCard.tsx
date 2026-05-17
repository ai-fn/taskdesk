import type { Task } from '@/types'
import { Priority, Status, PriorityLabel } from '@/types'
import { Button } from '@/components/ui/Button'
import { useDeleteTask, useUpdateStatus, useToggleFlagged, useRestoreTask, usePurgeTask } from '@/hooks/useTaskMutations'

const priorityColors: Record<Priority, string> = {
  [Priority.UNSPECIFIED]: 'bg-gray-100 text-gray-600',
  [Priority.LOW]: 'bg-blue-100 text-blue-700',
  [Priority.MEDIUM]: 'bg-yellow-100 text-yellow-700',
  [Priority.HIGH]: 'bg-orange-100 text-orange-700',
  [Priority.URGENT]: 'bg-red-100 text-red-700',
}

const statusSelectColors: Record<Status, string> = {
  [Status.UNSPECIFIED]: 'bg-gray-100 text-gray-600',
  [Status.TODO]: 'bg-slate-100 text-slate-700',
  [Status.IN_PROGRESS]: 'bg-purple-100 text-purple-700',
  [Status.DONE]: 'bg-green-100 text-green-700',
}

interface Props {
  task: Task
  onEdit: (task: Task) => void
  filterKey?: string
  isDeletedView?: boolean
}

export function TaskCard({ task, onEdit, filterKey, isDeletedView }: Props) {
  const del = useDeleteTask(filterKey)
  const updateStatus = useUpdateStatus(filterKey)
  const toggleFlagged = useToggleFlagged(filterKey)
  const restore = useRestoreTask(filterKey)
  const purge = usePurgeTask(filterKey)

  const isDone = task.status === Status.DONE
  const isTemp = task.id.startsWith('temp-')
  const isStatusPending = updateStatus.isPending && updateStatus.variables?.id === task.id

  return (
    <div className={`rounded-lg border bg-white p-4 shadow-sm transition-opacity ${isDone ? 'opacity-60' : ''}`}>
      <div className="flex items-start justify-between gap-3">
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-2">
            <p className={`font-medium text-gray-900 ${isDone ? 'line-through' : ''}`}>
              {task.title}
            </p>
            {task.flagged && <span className="text-red-500 text-sm">⚑</span>}
          </div>
          {task.description && (
            <p className="mt-1 text-sm text-gray-500 line-clamp-2">{task.description}</p>
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
                onChange={(e) =>
                  updateStatus.mutate({ id: task.id, status: Number(e.target.value) as Status })
                }
                className={`rounded-full px-2 py-0.5 text-xs font-medium cursor-pointer border-0 outline-none focus:ring-1 focus:ring-indigo-400 disabled:opacity-50 ${statusSelectColors[task.status]}`}
              >
                <option value={Status.TODO}>To Do</option>
                <option value={Status.IN_PROGRESS}>In Progress</option>
                <option value={Status.DONE}>Done</option>
              </select>
            )}
            {task.tags?.map((tag) => (
              <span key={tag} className="rounded-full bg-indigo-50 px-2 py-0.5 text-xs text-indigo-600">
                {tag}
              </span>
            ))}
            {task.due_at && (
              <span className="text-xs text-gray-400">
                Due {new Date(task.due_at).toLocaleDateString()}
              </span>
            )}
          </div>
        </div>

        {!isTemp && (
          <div className="flex shrink-0 gap-1">
            {isDeletedView ? (
              <>
                <Button
                  variant="ghost"
                  className="text-xs text-green-600 hover:text-green-800"
                  loading={restore.isPending && restore.variables === task.id}
                  onClick={() => restore.mutate(task.id)}
                >
                  Restore
                </Button>
                <Button
                  variant="ghost"
                  className="text-xs text-red-500 hover:text-red-700"
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
                      ? 'text-red-500 hover:text-red-700'
                      : 'text-gray-300 hover:text-red-400'
                  }`}
                  title={task.flagged ? 'Unflag' : 'Flag'}
                >
                  ⚑
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
                  className="text-xs text-red-500 hover:text-red-700"
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
