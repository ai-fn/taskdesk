import { useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { Task } from '@/types'
import { Status } from '@/types'
import { useTasks } from '@/hooks/useTasks'
import { useDependencies, useAddDependency, useRemoveDependency } from '@/hooks/useTaskDependencies'

interface Props {
  task: Task
}

export function DependencyPicker({ task }: Props) {
  const { t } = useTranslation()
  const { data: depData, isLoading } = useDependencies(task.id)
  const add = useAddDependency(task.id)
  const remove = useRemoveDependency(task.id)

  const [query, setQuery] = useState('')
  const [error, setError] = useState('')
  const [focused, setFocused] = useState(false)
  const inputRef = useRef<HTMLInputElement>(null)

  // Candidate pool: all the user's non-deleted tasks, minus current task and existing deps.
  const { data: allTasks } = useTasks({})
  const existingIds = useMemo(() => new Set((depData?.depends_on ?? []).map((t) => t.id)), [depData])
  const candidates = useMemo(() => {
    const pool = allTasks?.tasks ?? []
    return pool.filter((t) =>
      t.id !== task.id &&
      !existingIds.has(t.id) &&
      t.status !== Status.DONE && // unlikely useful to block on something already done
      t.title.toLowerCase().includes(query.trim().toLowerCase()),
    ).slice(0, 8)
  }, [allTasks, existingIds, query, task.id])

  function handleAdd(depId: string) {
    setError('')
    add.mutate(depId, {
      onSuccess: () => {
        setQuery('')
        inputRef.current?.focus()
      },
      onError: (err: unknown) => {
        const e = err as { response?: { data?: { error?: string } } }
        setError(e.response?.data?.error ?? t('tasks.deps.failedToAdd'))
      },
    })
  }

  return (
    <div className="border-t border-gray-100 dark:border-gray-700 pt-4">
      <p className="mb-2 text-sm font-medium text-gray-700 dark:text-gray-300">{t('tasks.detail.blockedBy')}</p>

      {isLoading && (
        <p className="text-xs text-gray-400 dark:text-gray-500 py-1">{t('common.loading')}</p>
      )}

      <div className="space-y-1">
        {(depData?.depends_on ?? []).map((d) => {
          const depDone = d.status === Status.DONE
          return (
            <div key={d.id} className="flex items-center gap-2 group py-0.5">
              <span className={`h-1.5 w-1.5 rounded-full shrink-0 ${depDone ? 'bg-green-500' : 'bg-amber-500'}`} />
              <span className={`flex-1 text-sm truncate ${depDone ? 'text-gray-400 line-through' : 'text-gray-700 dark:text-gray-300'}`}>
                {d.title}
              </span>
              <button
                type="button"
                onClick={() => remove.mutate(d.id)}
                disabled={remove.isPending}
                className="opacity-0 group-hover:opacity-100 shrink-0 text-gray-400 hover:text-red-500 dark:hover:text-red-400 transition-opacity disabled:opacity-50"
                title={t('tasks.deps.removeTitle')}
              >
                <svg viewBox="0 0 16 16" fill="none" className="w-3.5 h-3.5" stroke="currentColor" strokeWidth="2">
                  <path d="M4 4l8 8M12 4l-8 8" />
                </svg>
              </button>
            </div>
          )
        })}
      </div>

      <div className="relative pt-0.5">
        <div className="flex items-center gap-2">
          <span className="shrink-0 text-gray-300 dark:text-gray-600 text-xs">+</span>
          <input
            ref={inputRef}
            value={query}
            onChange={(e) => { setQuery(e.target.value); setError('') }}
            onFocus={() => setFocused(true)}
            onBlur={() => setTimeout(() => setFocused(false), 150)}
            placeholder={t('tasks.deps.addPlaceholder')}
            className="flex-1 bg-transparent text-sm text-gray-700 dark:text-gray-300 placeholder:text-gray-400 dark:placeholder:text-gray-500 outline-none border-b border-transparent focus:border-gray-200 dark:focus:border-gray-600 py-0.5"
          />
        </div>

        {focused && query.trim() && candidates.length > 0 && (
          <ul className="absolute z-10 mt-1 left-6 right-0 max-h-48 overflow-y-auto rounded-md border border-gray-200 dark:border-gray-600 bg-white dark:bg-gray-800 shadow-lg">
            {candidates.map((c) => (
              <li key={c.id}>
                <button
                  type="button"
                  onMouseDown={(e) => e.preventDefault()}
                  onClick={() => handleAdd(c.id)}
                  className="w-full text-left px-3 py-1.5 text-sm text-gray-700 dark:text-gray-300 hover:bg-indigo-50 dark:hover:bg-indigo-900/30 truncate"
                >
                  {c.title}
                </button>
              </li>
            ))}
          </ul>
        )}

        {focused && query.trim() && candidates.length === 0 && !isLoading && (
          <p className="mt-1 ml-6 text-xs text-gray-400 dark:text-gray-500">{t('tasks.deps.noMatching')}</p>
        )}
      </div>

      {error && <p className="mt-1 text-xs text-red-500 dark:text-red-400">{error}</p>}
    </div>
  )
}
