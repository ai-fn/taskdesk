import type { Task } from '@/types'
import { Priority, Status, PriorityLabel, StatusLabel } from '@/types'
import { Button } from '@/components/ui/Button'
import { useCompleteTask, useDeleteTask } from '@/hooks/useTaskMutations'

const priorityColors: Record<Priority, string> = {
  [Priority.UNSPECIFIED]: 'bg-gray-100 text-gray-600',
  [Priority.LOW]: 'bg-blue-100 text-blue-700',
  [Priority.MEDIUM]: 'bg-yellow-100 text-yellow-700',
  [Priority.HIGH]: 'bg-orange-100 text-orange-700',
  [Priority.URGENT]: 'bg-red-100 text-red-700',
}

const statusColors: Record<Status, string> = {
  [Status.UNSPECIFIED]: 'bg-gray-100 text-gray-600',
  [Status.TODO]: 'bg-slate-100 text-slate-700',
  [Status.IN_PROGRESS]: 'bg-purple-100 text-purple-700',
  [Status.DONE]: 'bg-green-100 text-green-700',
}

interface Props {
  task: Task
  onEdit: (task: Task) => void
}

export function TaskCard({ task, onEdit }: Props) {
  const complete = useCompleteTask()
  const del = useDeleteTask()

  const isDone = task.status === Status.DONE
  const isTemp = task.id.startsWith('temp-')

  return (
    <div className={`rounded-lg border bg-white p-4 shadow-sm transition-opacity ${isDone ? 'opacity-60' : ''}`}>
      <div className="flex items-start justify-between gap-3">
        <div className="flex-1 min-w-0">
          <p className={`font-medium text-gray-900 ${isDone ? 'line-through' : ''}`}>
            {task.title}
          </p>
          {task.description && (
            <p className="mt-1 text-sm text-gray-500 line-clamp-2">{task.description}</p>
          )}
          <div className="mt-2 flex flex-wrap items-center gap-2">
            {task.priority !== Priority.UNSPECIFIED && (
              <span className={`rounded-full px-2 py-0.5 text-xs font-medium ${priorityColors[task.priority]}`}>
                {PriorityLabel[task.priority]}
              </span>
            )}
            <span className={`rounded-full px-2 py-0.5 text-xs font-medium ${statusColors[task.status]}`}>
              {StatusLabel[task.status]}
            </span>
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
            {!isDone && (
              <Button
                variant="ghost"
                className="text-xs"
                loading={complete.isPending && complete.variables === task.id}
                onClick={() => complete.mutate(task.id)}
              >
                ✓
              </Button>
            )}
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
          </div>
        )}
      </div>
    </div>
  )
}
