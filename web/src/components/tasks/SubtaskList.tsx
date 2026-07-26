import { useState, useRef } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { listTasks } from '@/api/tasks'
import { taskKeys } from '@/api/queryKeys'
import type { Task } from '@/types'
import { Status } from '@/types'
import { useCompleteTask, useDeleteTask, useCreateTask } from '@/hooks/useTaskMutations'

interface Props {
  parentTask: Task
}

export function SubtaskList({ parentTask }: Props) {
  const { t } = useTranslation()
  const subtaskKey = `subtasks:${parentTask.id}`
  const qc = useQueryClient()

  const { data, isLoading } = useQuery({
    queryKey: taskKeys.list(subtaskKey),
    queryFn: () => listTasks({ parent_task_id: parentTask.id }),
    staleTime: 30_000,
  })

  const complete = useCompleteTask(subtaskKey)
  const del = useDeleteTask(subtaskKey)
  const create = useCreateTask()

  const [newTitle, setNewTitle] = useState('')
  const inputRef = useRef<HTMLInputElement>(null)

  function handleAdd() {
    const title = newTitle.trim()
    if (!title) return
    create.mutate(
      { title, parent_task_id: parentTask.id, list_id: parentTask.list_id },
      {
        onSuccess: () => {
          // Also refresh the parent list so subtask_count updates.
          qc.invalidateQueries({ queryKey: taskKeys.all })
        },
      }
    )
    setNewTitle('')
    inputRef.current?.focus()
  }

  const subtasks = data?.tasks ?? []

  return (
    <div className="mt-2 border-t border-gray-100 dark:border-gray-700 pt-2 space-y-1">
      {isLoading && (
        <p className="text-xs text-gray-400 dark:text-gray-500 py-1">{t('common.loading')}</p>
      )}

      {subtasks.map((st) => (
        <SubtaskRow
          key={st.id}
          task={st}
          onComplete={() => complete.mutate(st.id)}
          onDelete={() => del.mutate(st.id)}
          isCompletePending={complete.isPending && complete.variables === st.id}
          isDeletePending={del.isPending && del.variables === st.id}
        />
      ))}

      <div className="flex items-center gap-2 pt-0.5">
        <span className="shrink-0 text-gray-300 dark:text-gray-600 text-xs">+</span>
        <input
          ref={inputRef}
          value={newTitle}
          onChange={(e) => setNewTitle(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') handleAdd()
            if (e.key === 'Escape') setNewTitle('')
          }}
          placeholder={t('tasks.subtasks.addPlaceholder')}
          className="flex-1 bg-transparent text-xs text-gray-700 dark:text-gray-300 placeholder:text-gray-400 dark:placeholder:text-gray-500 outline-none border-b border-transparent focus:border-gray-200 dark:focus:border-gray-600 py-0.5"
        />
        {newTitle.trim() && (
          <button
            onClick={handleAdd}
            className="shrink-0 text-xs text-indigo-600 dark:text-indigo-400 hover:text-indigo-800 dark:hover:text-indigo-300"
          >
            {t('common.add')}
          </button>
        )}
      </div>
    </div>
  )
}

function SubtaskRow({
  task,
  onComplete,
  onDelete,
  isCompletePending,
  isDeletePending,
}: {
  task: Task
  onComplete: () => void
  onDelete: () => void
  isCompletePending: boolean
  isDeletePending: boolean
}) {
  const { t } = useTranslation()
  const isDone = task.status === Status.DONE

  return (
    <div className="flex items-center gap-2 group py-0.5">
      <button
        onClick={!isDone ? onComplete : undefined}
        disabled={isCompletePending}
        className={`shrink-0 flex items-center justify-center w-3.5 h-3.5 rounded border transition-colors disabled:opacity-50 ${
          isDone
            ? 'border-green-400 bg-green-400 dark:border-green-500 dark:bg-green-500 cursor-default'
            : 'border-gray-300 dark:border-gray-600 hover:border-green-400 dark:hover:border-green-500 cursor-pointer'
        }`}
      >
        {isDone && (
          <svg viewBox="0 0 10 10" fill="none" className="w-full h-full p-[1.5px] text-white">
            <path d="M2 5l2.5 2.5L8 3" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"/>
          </svg>
        )}
      </button>

      <span
        className={`flex-1 text-xs truncate ${
          isDone
            ? 'line-through text-gray-400 dark:text-gray-500'
            : 'text-gray-700 dark:text-gray-300'
        }`}
      >
        {task.title}
      </span>

      <button
        onClick={onDelete}
        disabled={isDeletePending}
        className="opacity-0 group-hover:opacity-100 shrink-0 text-gray-400 hover:text-red-500 dark:hover:text-red-400 transition-opacity disabled:opacity-50"
        title={t('tasks.subtasks.removeTitle')}
      >
        <svg viewBox="0 0 16 16" fill="none" className="w-3.5 h-3.5" stroke="currentColor" strokeWidth="2">
          <path d="M4 4l8 8M12 4l-8 8"/>
        </svg>
      </button>
    </div>
  )
}
