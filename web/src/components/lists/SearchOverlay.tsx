import { useState, useMemo } from 'react'
import { useNavigate } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { listTasks } from '@/api/tasks'
import { taskKeys } from '@/api/queryKeys'
import { useEscapeKey } from '@/hooks/useEscapeKey'
import type { Task, TaskList } from '@/types'

interface Props {
  lists: TaskList[]
  onClose: () => void
}

export function SearchOverlay({ lists, onClose }: Props) {
  const [query, setQuery] = useState('')
  const navigate = useNavigate()
  useEscapeKey(onClose)

  const { data } = useQuery({
    queryKey: taskKeys.list('search-all'),
    queryFn: () => listTasks(),
    staleTime: 60_000,
  })

  const allTasks = data?.tasks ?? []

  const listMap = useMemo(
    () => Object.fromEntries(lists.map((l) => [l.id, l])),
    [lists]
  )

  // Tasks grouped by list, filtered by query (title, description, or list name)
  const grouped = useMemo(() => {
    if (!query.trim()) return []

    const lower = query.toLowerCase()

    // List IDs whose name matches the query
    const matchingListIds = new Set(
      lists.filter((l) => l.name.toLowerCase().includes(lower)).map((l) => l.id)
    )

    const matching = allTasks.filter(
      (t) =>
        !t.deleted_at &&
        (t.title.toLowerCase().includes(lower) ||
          (t.description?.toLowerCase().includes(lower) ?? false) ||
          (t.list_id != null && matchingListIds.has(t.list_id)))
    )

    // Group: list_id → tasks; null key = no list
    const map = new Map<string | null, Task[]>()
    for (const task of matching) {
      const key = task.list_id ?? null
      if (!map.has(key)) map.set(key, [])
      map.get(key)!.push(task)
    }

    // Sort: named lists first (by list position), then no-list last
    return Array.from(map.entries()).sort(([a], [b]) => {
      if (a === null) return 1
      if (b === null) return -1
      const posA = listMap[a]?.position ?? 0
      const posB = listMap[b]?.position ?? 0
      return posA - posB
    })
  }, [allTasks, query, lists, listMap])

  const totalCount = grouped.reduce((n, [, tasks]) => n + tasks.length, 0)

  function handleSelect(task: Task) {
    navigate(task.list_id ? `/tasks/${task.list_id}` : '/tasks/all')
    onClose()
  }

  return (
    <div className="fixed inset-0 z-50 flex flex-col bg-white/95 dark:bg-gray-900/95 backdrop-blur-sm">
      <div className="flex items-center gap-2 border-b border-gray-200 dark:border-gray-700 px-4 py-3">
        <span className="text-gray-400">🔍</span>
        <input
          autoFocus
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="Search tasks and lists..."
          className="flex-1 bg-transparent text-base outline-none placeholder:text-gray-400 dark:placeholder:text-gray-500 text-gray-900 dark:text-gray-100"
        />
        <button onClick={onClose} className="text-sm text-indigo-600 dark:text-indigo-400 font-medium">
          Cancel
        </button>
      </div>

      <div className="flex-1 overflow-y-auto">
        {!query.trim() && (
          <p className="px-4 py-8 text-center text-sm text-gray-400 dark:text-gray-500">
            Search by task name, description or list name
          </p>
        )}
        {query.trim() && totalCount === 0 && (
          <p className="px-4 py-8 text-center text-sm text-gray-400 dark:text-gray-500">No results</p>
        )}
        {grouped.map(([listId, tasks]) => {
          const list = listId ? listMap[listId] : undefined
          return (
            <div key={listId ?? 'no-list'}>
              {/* List header */}
              <div className="sticky top-0 flex items-center gap-2 border-b border-gray-200 dark:border-gray-700 bg-gray-50 dark:bg-gray-800 px-4 py-2">
                {list ? (
                  <>
                    <span
                      className="h-2.5 w-2.5 rounded-full flex-shrink-0"
                      style={{ backgroundColor: list.color }}
                    />
                    <span className="text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">
                      {list.name}
                    </span>
                  </>
                ) : (
                  <span className="text-xs font-semibold uppercase tracking-wide text-gray-400 dark:text-gray-500">
                    No list
                  </span>
                )}
                <span className="ml-auto text-xs text-gray-400 dark:text-gray-500">{tasks.length}</span>
              </div>

              {/* Tasks in this group */}
              {tasks.map((task) => (
                <button
                  key={task.id}
                  onClick={() => handleSelect(task)}
                  className="flex w-full flex-col gap-0.5 border-b border-gray-100 dark:border-gray-800 px-6 py-3 text-left hover:bg-gray-50 dark:hover:bg-gray-800"
                >
                  <span className="text-sm font-medium text-gray-900 dark:text-gray-100">{task.title}</span>
                  {task.description && (
                    <span className="line-clamp-1 text-xs text-gray-400 dark:text-gray-500">
                      {task.description}
                    </span>
                  )}
                </button>
              ))}
            </div>
          )
        })}
      </div>
    </div>
  )
}
