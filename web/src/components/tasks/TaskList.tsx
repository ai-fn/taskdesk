import type { Task } from '@/types'
import { TaskCard } from './TaskCard'
import { Spinner } from '@/components/ui/Spinner'
import { ErrorMessage } from '@/components/ui/ErrorMessage'
import { useTasks } from '@/hooks/useTasks'

interface Props {
  onEdit: (task: Task) => void
}

export function TaskList({ onEdit }: Props) {
  const { data, isLoading, isError, error } = useTasks()

  if (isLoading) {
    return (
      <div className="flex justify-center py-12">
        <Spinner className="h-8 w-8 text-indigo-500" />
      </div>
    )
  }

  if (isError) {
    return <ErrorMessage message={(error as Error).message ?? 'Failed to load tasks'} />
  }

  const tasks = data?.tasks ?? []

  if (tasks.length === 0) {
    return (
      <p className="py-12 text-center text-gray-400">
        No tasks yet. Create your first one above.
      </p>
    )
  }

  return (
    <div className="flex flex-col gap-3">
      {tasks.map((task) => (
        <TaskCard key={task.id} task={task} onEdit={onEdit} />
      ))}
    </div>
  )
}
