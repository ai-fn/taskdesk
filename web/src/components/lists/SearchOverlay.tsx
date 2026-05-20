import { useState, useMemo, useEffect, useRef } from 'react'
import { useNavigate } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { listTasks, searchTasks } from '@/api/tasks'
import { taskKeys } from '@/api/queryKeys'
import type { Task, TaskList } from '@/types'

function scoreColor(score: number) {
  if (score >= 70) return 'bg-green-100 text-green-700 dark:bg-green-900/40 dark:text-green-300'
  if (score >= 40) return 'bg-yellow-100 text-yellow-700 dark:bg-yellow-900/40 dark:text-yellow-300'
  return 'bg-red-100 text-red-700 dark:bg-red-900/40 dark:text-red-300'
}

const SEMANTIC_MIN = 3
const DEBOUNCE_MS = 350

interface Props {
  lists: TaskList[]
}

export function SearchBar({ lists }: Props) {
  const [query, setQuery] = useState('')
  const [debouncedQuery, setDebouncedQuery] = useState('')
  const [isOpen, setIsOpen] = useState(false)
  const containerRef = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLInputElement>(null)
  const navigate = useNavigate()

  useEffect(() => {
    const id = setTimeout(() => setDebouncedQuery(query.trim()), DEBOUNCE_MS)
    return () => clearTimeout(id)
  }, [query])

  useEffect(() => {
    function onMouseDown(e: MouseEvent) {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) {
        setIsOpen(false)
      }
    }
    document.addEventListener('mousedown', onMouseDown)
    return () => document.removeEventListener('mousedown', onMouseDown)
  }, [])

  const isSemantic = debouncedQuery.length >= SEMANTIC_MIN

  const { data: localData } = useQuery({
    queryKey: taskKeys.list('search-all'),
    queryFn: () => listTasks(),
    staleTime: 60_000,
  })

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
    setQuery('')
    setIsOpen(false)
    inputRef.current?.blur()
  }

  function handleKeyDown(e: React.KeyboardEvent) {
    if (e.key === 'Escape') {
      setQuery('')
      setIsOpen(false)
      inputRef.current?.blur()
    }
  }

  function handleClear() {
    setQuery('')
    setIsOpen(false)
    inputRef.current?.focus()
  }

  const showDropdown = isOpen && query.length > 0

  return (
    <div ref={containerRef} className="relative mb-6">
      <div className="flex items-center gap-2 rounded-xl bg-white dark:bg-gray-800 px-4 py-2.5 shadow-sm ring-1 ring-transparent focus-within:ring-indigo-400/60 transition-shadow">
        <span className="shrink-0 text-gray-400">
          {isSearching ? (
            <svg className="h-4 w-4 animate-spin" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
              <path d="M21 12a9 9 0 11-18 0 9 9 0 0118 0z" strokeOpacity="0.3"/>
              <path d="M12 3a9 9 0 019 9"/>
            </svg>
          ) : (
            <svg className="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
              <circle cx="11" cy="11" r="8"/>
              <path d="m21 21-4.35-4.35"/>
            </svg>
          )}
        </span>
        <input
          ref={inputRef}
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          onFocus={() => setIsOpen(true)}
          onKeyDown={handleKeyDown}
          placeholder="Search tasks..."
          className="flex-1 bg-transparent text-sm outline-none placeholder:text-gray-400 dark:placeholder:text-gray-500 text-gray-900 dark:text-gray-100"
        />
        {query && (
          <button
            onMouseDown={(e) => e.preventDefault()}
            onClick={handleClear}
            className="shrink-0 text-gray-400 hover:text-gray-600 dark:hover:text-gray-300"
          >
            <svg className="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
              <path d="M18 6 6 18M6 6l12 12"/>
            </svg>
          </button>
        )}
      </div>

      {showDropdown && (
        <div className="absolute left-0 right-0 top-full mt-1.5 z-50 rounded-xl border border-gray-200 dark:border-gray-700 bg-white dark:bg-gray-800 shadow-xl overflow-hidden">
          {isSemantic && (
            <div className="flex items-center gap-1.5 border-b border-gray-100 dark:border-gray-700 bg-indigo-50/60 dark:bg-indigo-950/20 px-3 py-1.5 text-xs text-indigo-500 dark:text-indigo-400">
              <svg className="h-3 w-3" viewBox="0 0 24 24" fill="currentColor">
                <path d="M12 2L2 7l10 5 10-5-10-5zM2 17l10 5 10-5M2 12l10 5 10-5"/>
              </svg>
              Semantic · ranked by meaning
            </div>
          )}

          <div className="max-h-80 overflow-y-auto">
            {debouncedQuery && !isSearching && results.length === 0 && (
              <p className="px-4 py-5 text-center text-xs text-gray-400 dark:text-gray-500">No results</p>
            )}

            {results.map((task: Task) => {
              const list = task.list_id ? listMap[task.list_id] : undefined
              const listColor = list?.color || '#8E8E93'
              return (
                <button
                  key={task.id}
                  onMouseDown={(e) => e.preventDefault()}
                  onClick={() => handleSelect(task)}
                  className="flex w-full flex-col gap-0.5 border-b border-gray-100 dark:border-gray-700/50 px-4 py-2.5 text-left last:border-b-0 hover:bg-gray-50 dark:hover:bg-gray-700/50"
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
                    {task.completion_score != null && task.completion_score > 0 && (
                      <span
                        className={`shrink-0 rounded-full px-1.5 py-0.5 text-xs font-medium ${scoreColor(task.completion_score)}`}
                        title={task.completion_insight || undefined}
                      >
                        ✦ {task.completion_score}%
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
      )}
    </div>
  )
}
