import { useState, useMemo, useEffect } from 'react'
import { useNavigate } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { listTasks, searchTasks } from '@/api/tasks'
import { taskKeys } from '@/api/queryKeys'
import { useEscapeKey } from '@/hooks/useEscapeKey'
import type { Task, TaskList } from '@/types'

// Minimum query length to trigger semantic search (Voyage AI call).
const SEMANTIC_MIN = 3
// Debounce delay for semantic queries (ms).
const DEBOUNCE_MS = 350

interface Props {
  lists: TaskList[]
  onClose: () => void
}

export function SearchOverlay({ lists, onClose }: Props) {
  const [query, setQuery] = useState('')
  const [debouncedQuery, setDebouncedQuery] = useState('')
  const navigate = useNavigate()
  useEscapeKey(onClose)

  // Debounce: only update debouncedQuery after the user stops typing.
  useEffect(() => {
    const id = setTimeout(() => setDebouncedQuery(query.trim()), DEBOUNCE_MS)
    return () => clearTimeout(id)
  }, [query])

  const isSemantic = debouncedQuery.length >= SEMANTIC_MIN

  // Local cache: all non-deleted tasks — used for substring fallback.
  const { data: localData } = useQuery({
    queryKey: taskKeys.list('search-all'),
    queryFn: () => listTasks(),
    staleTime: 60_000,
  })

  // Semantic search: only fires when query is long enough.
  const { data: semanticData, isFetching: isSearching } = useQuery({
    queryKey: taskKeys.list(`semantic:${debouncedQuery}`),
    queryFn: () => searchTasks(debouncedQuery),
    enabled: isSemantic,
    staleTime: 30_000,
  })

  const listMap = useMemo(
    () => Object.fromEntries(lists.map((l) => [l.id, l])),
    [lists]
  )

  const results = useMemo(() => {
    if (!debouncedQuery) return []

    if (isSemantic) {
      return (semanticData?.tasks ?? []).filter((t: Task) => !t.deleted_at)
    }

    const lower = debouncedQuery.toLowerCase()
    const matchingListIds = new Set(
      lists.filter((l) => l.name.toLowerCase().includes(lower)).map((l) => l.id)
    )
    return (localData?.tasks ?? []).filter(
      (t) =>
        !t.deleted_at &&
        (t.title.toLowerCase().includes(lower) ||
          (t.description?.toLowerCase().includes(lower) ?? false) ||
          (!!t.list_id && matchingListIds.has(t.list_id)))
    )
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [debouncedQuery, isSemantic, semanticData, localData, lists])

  function handleSelect(task: Task) {
    navigate(task.list_id || null ? `/tasks/${task.list_id}` : '/tasks/all')
    onClose()
  }

  return (
    <div className="fixed inset-0 z-50 flex flex-col bg-white/95 dark:bg-gray-900/95 backdrop-blur-sm">
      <div className="flex items-center gap-2 border-b border-gray-200 dark:border-gray-700 px-4 py-3">
        <span className="text-gray-400">
          {isSearching ? (
            <svg className="h-4 w-4 animate-spin" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
              <path d="M21 12a9 9 0 11-18 0 9 9 0 0118 0z" strokeOpacity="0.3"/>
              <path d="M12 3a9 9 0 019 9"/>
            </svg>
          ) : '🔍'}
        </span>
        <input
          autoFocus
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="Search tasks semantically..."
          className="flex-1 bg-transparent text-base outline-none placeholder:text-gray-400 dark:placeholder:text-gray-500 text-gray-900 dark:text-gray-100"
        />
        <button onClick={onClose} className="text-sm text-indigo-600 dark:text-indigo-400 font-medium">
          Cancel
        </button>
      </div>

      <div className="flex-1 overflow-y-auto">
        {!debouncedQuery && (
          <p className="px-4 py-8 text-center text-sm text-gray-400 dark:text-gray-500">
            Type 3+ characters for semantic search — finds tasks by meaning, not just keywords
          </p>
        )}
        {debouncedQuery && !isSearching && results.length === 0 && (
          <p className="px-4 py-8 text-center text-sm text-gray-400 dark:text-gray-500">No results</p>
        )}

        {isSemantic && debouncedQuery && (
          <div className="px-4 py-1.5 flex items-center gap-1.5 text-xs text-indigo-500 dark:text-indigo-400 border-b border-gray-100 dark:border-gray-800">
            <svg className="h-3 w-3" viewBox="0 0 24 24" fill="currentColor">
              <path d="M12 2L2 7l10 5 10-5-10-5zM2 17l10 5 10-5M2 12l10 5 10-5"/>
            </svg>
            Semantic search · ranked by meaning
          </div>
        )}

        {results.map((task: Task) => {
          const list = task.list_id ? listMap[task.list_id] : undefined
          const listColor = list?.color || '#8E8E93'
          return (
            <button
              key={task.id}
              onClick={() => handleSelect(task)}
              className="flex w-full flex-col gap-1 border-b border-gray-100 dark:border-gray-800 px-4 py-3 text-left hover:bg-gray-50 dark:hover:bg-gray-800"
            >
              <div className="flex items-center gap-2 min-w-0">
                <span className="truncate text-sm font-medium text-gray-900 dark:text-gray-100">
                  {task.title}
                </span>
                {list && (
                  <span
                    className="shrink-0 rounded px-1.5 py-0.5 text-xs font-medium"
                    style={{ color: listColor, backgroundColor: listColor + '22' }}
                  >
                    {list.name}
                  </span>
                )}
              </div>
              {task.description && (
                <span className="truncate text-xs text-gray-400 dark:text-gray-500">
                  {task.description}
                </span>
              )}
            </button>
          )
        })}
      </div>
    </div>
  )
}
