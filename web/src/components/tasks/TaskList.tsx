import { useTranslation } from 'react-i18next'
import type { Task } from '@/types'
import type { ListTasksFilter } from '@/api/tasks'
import { TaskCard } from './TaskCard'
import { Spinner } from '@/components/ui/Spinner'
import { ErrorMessage } from '@/components/ui/ErrorMessage'
import { useTasks } from '@/hooks/useTasks'

interface Props {
  onEdit: (task: Task) => void
  filter?: ListTasksFilter
  filterKey?: string
  isDeletedView?: boolean
  searchQuery?: string
}

export function TaskList({ onEdit, filter, filterKey, isDeletedView, searchQuery }: Props) {
  const { t } = useTranslation()
  const { data, isLoading, isError, error } = useTasks(filter)

  if (isLoading) {
    return (
      <div className="flex justify-center py-12">
        <Spinner className="h-8 w-8 text-indigo-500" />
      </div>
    )
  }

  if (isError) {
    return <ErrorMessage message={(error as Error).message ?? t('tasks.list.failedToLoad')} />
  }

  const allTasks = data?.tasks ?? []
  const lower = searchQuery?.toLowerCase() ?? ''
  const tasks = lower
    ? allTasks.filter(
        (t) =>
          t.title.toLowerCase().includes(lower) ||
          (t.description?.toLowerCase().includes(lower) ?? false)
      )
    : allTasks

  if (allTasks.length === 0) {
    return (
      <p className="py-12 text-center text-gray-400 dark:text-gray-500">
        {isDeletedView ? t('tasks.list.noDeletedTasks') : t('tasks.list.empty')}
      </p>
    )
  }

  if (tasks.length === 0) {
    return (
      <p className="py-12 text-center text-gray-400 dark:text-gray-500">No matching tasks.</p>
    )
  }

  return (
    <div className="flex flex-col gap-3">
      {data?.next_page_token && (
        <p role="status" className="text-center text-sm text-amber-700 dark:text-amber-400">
          {t('tasks.list.truncated', { limit: allTasks.length })}
        </p>
      )}
      {tasks.map((task) => (
        <TaskCard
          key={task.id}
          task={task}
          onEdit={onEdit}
          filterKey={filterKey}
          isDeletedView={isDeletedView}
        />
      ))}
    </div>
  )
}
