import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useQuery } from '@tanstack/react-query'
import { listTasks } from '@/api/tasks'
import { taskKeys } from '@/api/queryKeys'
import { useLists, useCreateList } from '@/hooks/useLists'
import { useGroups } from '@/hooks/useGroups'
import { useCreateTask } from '@/hooks/useTaskMutations'
import { useBodyScrollLock } from '@/hooks/useBodyScrollLock'
import { SYSTEM_LISTS } from '@/types'
import type { ListGroup, TaskList } from '@/types'
import { ListCard, SystemListCard } from '@/components/lists/ListCard'
import { ListFormModal } from '@/components/lists/ListFormModal'
import { EditListsPanel } from '@/components/lists/EditListsPanel'
import { SearchBar } from '@/components/lists/SearchOverlay'
import { DailyPlanPanel } from '@/components/tasks/DailyPlanPanel'
import { TaskForm, type TaskPrefill } from '@/components/tasks/TaskForm'
import { NaturalLanguageInput } from '@/components/tasks/NaturalLanguageInput'
import { Spinner } from '@/components/ui/Spinner'
import { Modal } from '@/components/ui/Modal'

function useSystemCount(filter: Parameters<typeof listTasks>[0]) {
  return useQuery({
    queryKey: taskKeys.list(JSON.stringify(filter)),
    queryFn: () => listTasks(filter),
    staleTime: 60_000,
    select: (data) => data.tasks?.length ?? 0,
  })
}

export function ListsHomePage() {
  const { t } = useTranslation()
  const [showEdit, setShowEdit] = useState(false)
  const [showCreate, setShowCreate] = useState(false)
  const [showCreateTask, setShowCreateTask] = useState(false)
  const [showNLTask, setShowNLTask] = useState(false)
  const [nlPrefill, setNlPrefill] = useState<TaskPrefill | undefined>()

  const { data: lists = [], isLoading: listsLoading } = useLists()
  const { data: groups = [], isLoading: groupsLoading } = useGroups()
  const createList = useCreateList()
  const createTask = useCreateTask()

  const { data: countAll = 0 }       = useSystemCount({})
  const { data: countToday = 0 }     = useSystemCount({ due_today: true })
  const { data: countScheduled = 0 } = useSystemCount({ has_due_date: true })
  const { data: countFlagged = 0 }   = useSystemCount({ flagged_only: true })
  const { data: countCompleted = 0 } = useQuery({
    queryKey: taskKeys.list(JSON.stringify({ status: 3 })),
    queryFn: () => listTasks(),
    staleTime: 60_000,
    select: (data) => (data.tasks ?? []).filter((t) => t.status === 3).length,
  })
  const { data: countDeleted = 0 }   = useSystemCount({ include_deleted: true })

  const systemCounts: Record<string, number> = {
    all:       countAll,
    today:     countToday,
    scheduled: countScheduled,
    flagged:   countFlagged,
    completed: countCompleted,
    deleted:   countDeleted,
  }

  useBodyScrollLock(showCreateTask || showCreate || showEdit || showNLTask)

  const isLoading = listsLoading || groupsLoading
  const visibleLists = lists.filter((l) => !l.is_hidden)

  // Map each group_id → visible lists in that group
  const listsByGroup = new Map<string | null, TaskList[]>()
  listsByGroup.set(null, [])
  for (const g of groups) listsByGroup.set(g.id, [])
  for (const l of visibleLists) {
    const key = l.group_id && listsByGroup.has(l.group_id) ? l.group_id : null
    listsByGroup.get(key)!.push(l)
  }

  // Sections in display order: named groups first, ungrouped last
  const sections: Array<{ group: ListGroup | null; lists: TaskList[] }> = groups.length > 0
    ? [
        ...groups.map((g) => ({ group: g, lists: listsByGroup.get(g.id) ?? [] })),
        ...(listsByGroup.get(null)!.length > 0 ? [{ group: null, lists: listsByGroup.get(null)! }] : []),
      ]
    : (visibleLists.length > 0 ? [{ group: null, lists: visibleLists }] : [])

  return (
    <div className="min-h-screen bg-gray-100 dark:bg-gray-950">
      <div className="mx-auto max-w-2xl px-4 py-6">
        {/* Header */}
        <div className="mb-4 flex items-center justify-between">
          <div className="flex items-center gap-1">
            <button
              onClick={() => setShowEdit(true)}
              className="rounded-lg p-2 text-gray-500 dark:text-gray-400 hover:bg-gray-200 dark:hover:bg-gray-800"
              title={t('editLists.title')}
            >
              ☰
            </button>
            <button
              onClick={() => setShowNLTask(true)}
              className="rounded-lg p-1.5 text-gray-400 hover:text-indigo-500 dark:hover:text-indigo-400 transition-colors"
              title={t('tasks.parse.nlTooltip')}
            >
              ✨
            </button>
            <button
              onClick={() => setShowCreateTask(true)}
              className="rounded-lg px-3 py-1.5 text-sm font-medium text-indigo-600 dark:text-indigo-400 hover:bg-indigo-50 dark:hover:bg-indigo-900/30"
            >
              {t('lists.home.newTask')}
            </button>
          </div>
          <h1 className="text-xl font-bold text-gray-900 dark:text-gray-100">{t('lists.home.myLists')}</h1>
          <button
            onClick={() => setShowCreate(true)}
            className="rounded-lg px-3 py-1.5 text-sm font-medium text-indigo-600 dark:text-indigo-400 hover:bg-indigo-50 dark:hover:bg-indigo-900/30"
          >
            {t('lists.home.newList')}
          </button>
        </div>

        {/* Search bar */}
        <SearchBar lists={lists} />

        {/* AI daily plan */}
        <DailyPlanPanel />

        {/* System lists grid */}
        <div className="mb-6 grid grid-cols-2 gap-3">
          {Object.values(SYSTEM_LISTS).map((sl) => (
            <SystemListCard
              key={sl.id}
              id={sl.id}
              name={t(sl.nameKey)}
              color={sl.color}
              icon={sl.icon}
              count={systemCounts[sl.id] ?? 0}
              href={`/tasks/${sl.id}`}
            />
          ))}
        </div>

        {/* Custom lists grouped */}
        {isLoading ? (
          <div className="flex justify-center py-8">
            <Spinner className="h-6 w-6 text-indigo-500" />
          </div>
        ) : sections.map(({ group, lists: sectionLists }) =>
          sectionLists.length === 0 ? null : (
            <div key={group?.id ?? '__ungrouped'} className="mb-6">
              <h2 className="mb-3 text-xs font-semibold uppercase tracking-wide text-gray-400 dark:text-gray-500">
                {group?.name ?? t('lists.home.myLists')}
              </h2>
              <div className="grid grid-cols-2 gap-3">
                {sectionLists.map((list) => (
                  <ListCard key={list.id} list={list} href={`/tasks/${list.id}`} />
                ))}
              </div>
            </div>
          )
        )}
      </div>

      {showEdit && (
        <EditListsPanel
          lists={lists}
          groups={groups}
          onClose={() => setShowEdit(false)}
          onCreateNew={() => { setShowEdit(false); setShowCreate(true) }}
        />
      )}

      {showCreate && (
        <ListFormModal
          groups={groups}
          onSave={(name, color, groupId, icon) => {
            createList.mutate({ name, color, group_id: groupId, icon })
            setShowCreate(false)
          }}
          onCancel={() => setShowCreate(false)}
          isLoading={createList.isPending}
        />
      )}

      {showCreateTask && (
        <Modal onClose={() => { setShowCreateTask(false); setNlPrefill(undefined) }} className="max-w-xl">
          <div className="rounded-xl bg-white dark:bg-gray-800 shadow-xl overflow-hidden">
            <div className="max-h-[90vh] overflow-y-auto p-6">
              <h2 className="mb-4 text-lg font-semibold text-gray-900 dark:text-gray-100">New task</h2>
              <TaskForm
                prefill={nlPrefill}
                lists={lists}
                onSubmit={async (values) => {
                  const { list_id, subtasks = [], ...rest } = values
                  const newTask = await createTask.mutateAsync({ ...rest, list_id: list_id ?? undefined })
                  if (subtasks.length > 0) {
                    await Promise.all(subtasks.map((title) =>
                      createTask.mutateAsync({ title, parent_task_id: newTask.id, list_id: newTask.list_id ?? undefined })
                    ))
                  }
                  setShowCreateTask(false)
                  setNlPrefill(undefined)
                }}
                onCancel={() => { setShowCreateTask(false); setNlPrefill(undefined) }}
                isLoading={createTask.isPending}
              />
            </div>
          </div>
        </Modal>
      )}

      {showNLTask && (
        <Modal onClose={() => setShowNLTask(false)} className="max-w-md">
          <div className="rounded-xl bg-white dark:bg-gray-800 p-6 shadow-xl">
            <h2 className="mb-4 text-lg font-semibold text-gray-900 dark:text-gray-100">New task</h2>
            <NaturalLanguageInput
              lists={lists}
              onParsed={(prefill) => {
                setNlPrefill(prefill)
                setShowNLTask(false)
                setShowCreateTask(true)
              }}
              onCancel={() => setShowNLTask(false)}
            />
          </div>
        </Modal>
      )}
    </div>
  )
}
