import { useCallback, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useQuery } from '@tanstack/react-query'
import { listTasks } from '@/api/tasks'
import { taskKeys } from '@/api/queryKeys'
import { useLists, useCreateList } from '@/hooks/useLists'
import { useGroups } from '@/hooks/useGroups'
import { useCreateTask } from '@/hooks/useTaskMutations'
import { useBodyScrollLock } from '@/hooks/useBodyScrollLock'
import { useAuth } from '@/hooks/useAuth'
import { FREE_MAX_FILE_BYTES } from '@/hooks/useAttachments'
import { createUploadURL, putToStorage, confirmAttachment } from '@/api/attachments'
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
import { ToastContainer, type ToastMessage } from '@/components/ui/Toast'

type TaskMode = 'quick' | 'ai'

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
  const [taskMode, setTaskMode] = useState<TaskMode>('quick')
  const [nlPrefill, setNlPrefill] = useState<TaskPrefill | undefined>()
  const [uploadProgress, setUploadProgress] = useState<Record<string, number> | undefined>(undefined)
  const [toasts, setToasts] = useState<ToastMessage[]>([])

  const { data: lists = [], isLoading: listsLoading } = useLists()
  const { data: groups = [], isLoading: groupsLoading } = useGroups()
  const { plan } = useAuth()
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

  useBodyScrollLock(showCreateTask || showCreate || showEdit)

  const isLoading = listsLoading || groupsLoading
  const visibleLists = lists.filter((l) => !l.is_hidden)

  const listsByGroup = new Map<string | null, TaskList[]>()
  listsByGroup.set(null, [])
  for (const g of groups) listsByGroup.set(g.id, [])
  for (const l of visibleLists) {
    const key = l.group_id && listsByGroup.has(l.group_id) ? l.group_id : null
    listsByGroup.get(key)!.push(l)
  }

  const sections: Array<{ group: ListGroup | null; lists: TaskList[] }> = groups.length > 0
    ? [
        ...groups.map((g) => ({ group: g, lists: listsByGroup.get(g.id) ?? [] })),
        ...(listsByGroup.get(null)!.length > 0 ? [{ group: null, lists: listsByGroup.get(null)! }] : []),
      ]
    : (visibleLists.length > 0 ? [{ group: null, lists: visibleLists }] : [])

  function openTaskModal(mode: TaskMode = 'quick') {
    setTaskMode(mode)
    setNlPrefill(undefined)
    setShowCreateTask(true)
  }

  function closeTaskModal() {
    setShowCreateTask(false)
    setTaskMode('quick')
    setNlPrefill(undefined)
    setUploadProgress(undefined)
  }

  const dismissToast = useCallback((id: number) => {
    setToasts((prev) => prev.filter((t) => t.id !== id))
  }, [])

  async function handleCreateTask(values: { title: string; description: string; priority: number; status?: number; tags: string[]; due_at?: string; list_id?: string | null; flagged?: boolean; subtasks?: string[]; files?: File[] }) {
    const { list_id, subtasks = [], files = [], ...rest } = values
    const newTask = await createTask.mutateAsync({ ...rest, list_id: list_id ?? undefined })
    if (subtasks.length > 0) {
      await Promise.all(subtasks.map((title) =>
        createTask.mutateAsync({ title, parent_task_id: newTask.id, list_id: newTask.list_id ?? undefined })
      ))
    }
    if (files.length > 0) {
      setUploadProgress({})
      for (const file of files) {
        if (plan === 'free' && file.size > FREE_MAX_FILE_BYTES) {
          setToasts((prev) => [...prev, { id: Date.now(), text: t('attachments.errorTooLargeFree', { max: '10 MiB' }) }])
          continue
        }
        try {
          setUploadProgress((p) => ({ ...(p ?? {}), [file.name]: 0 }))
          const { upload_url, attachment_id } = await createUploadURL(newTask.id, {
            filename: file.name,
            content_type: file.type || 'application/octet-stream',
            size_bytes: file.size,
          })
          await putToStorage(upload_url, file, file.type || 'application/octet-stream', (percent) => {
            setUploadProgress((p) => ({ ...(p ?? {}), [file.name]: percent }))
          })
          await confirmAttachment(newTask.id, attachment_id)
        } catch {
          setToasts((prev) => [...prev, { id: Date.now(), text: `${file.name}: ${t('attachments.skipToDetail')}` }])
        } finally {
          setUploadProgress((p) => {
            if (!p) return p
            const next = { ...p }
            delete next[file.name]
            return next
          })
        }
      }
    }
    closeTaskModal()
  }

  return (
    <>
    <ToastContainer toasts={toasts} onDismiss={dismissToast} />
    <div className="min-h-screen bg-gray-100 dark:bg-gray-950">
      <div className="mx-auto max-w-2xl px-4 py-6 pb-24">

        {/* ── Header ─────────────────────────────────────────────────────────
            Left: ☰ (structure control — organizes the lists below)
            Center-left: title
            Right: + list (creates a list item in the collection)
        ──────────────────────────────────────────────────────────────────── */}
        <div className="mb-4 flex items-center gap-2">
          <button
            onClick={() => setShowEdit(true)}
            className="rounded-lg p-2 text-gray-500 dark:text-gray-400 hover:bg-gray-200 dark:hover:bg-gray-800 transition-colors"
            title={t('editLists.title')}
          >
            <svg className="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M3.75 6.75h16.5M3.75 12h16.5m-16.5 5.25h16.5" />
            </svg>
          </button>
          <h1 className="flex-1 text-xl font-bold text-gray-900 dark:text-gray-100">
            {t('lists.home.myLists')}
          </h1>
          <button
            onClick={() => setShowCreate(true)}
            className="rounded-lg p-2 text-gray-500 dark:text-gray-400 hover:bg-gray-200 dark:hover:bg-gray-800 transition-colors"
            title={t('lists.home.newList')}
          >
            <svg className="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M12 10.5v6m3-3H9m4.06-7.19l-2.12-2.12a1.5 1.5 0 00-1.061-.44H4.5A2.25 2.25 0 002.25 6v12a2.25 2.25 0 002.25 2.25h15A2.25 2.25 0 0021.75 18V9a2.25 2.25 0 00-2.25-2.25h-5.379a1.5 1.5 0 01-1.06-.44z" />
            </svg>
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

      {/* ── FAB ── */}
      <button
        onClick={() => openTaskModal('quick')}
        aria-label={t('lists.home.newTask')}
        className="fixed bottom-6 right-6 z-40 flex h-14 w-14 items-center justify-center rounded-full bg-indigo-600 dark:bg-indigo-500 text-white shadow-lg hover:bg-indigo-700 dark:hover:bg-indigo-600 active:scale-95 transition-all"
      >
        <svg className="h-7 w-7" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2.5}>
          <path strokeLinecap="round" strokeLinejoin="round" d="M12 4.5v15m7.5-7.5h-15" />
        </svg>
      </button>

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

      {/* ── Unified New Task modal (Quick + AI Parse tabs) ── */}
      {showCreateTask && (
        <Modal onClose={closeTaskModal} className="max-w-xl">
          <div className="rounded-xl bg-white dark:bg-gray-800 shadow-xl overflow-hidden">
            {/* Modal header with mode tabs */}
            <div className="flex items-center justify-between px-6 pt-5 pb-4 border-b border-gray-100 dark:border-gray-700/60">
              <h2 className="text-lg font-semibold text-gray-900 dark:text-gray-100">
                {t('tasks.form.newTask')}
              </h2>
              <div className="flex gap-1 rounded-lg bg-gray-100 dark:bg-gray-700 p-1">
                <button
                  onClick={() => setTaskMode('quick')}
                  className={`rounded-md px-3 py-1 text-xs font-medium transition-all ${
                    taskMode === 'quick'
                      ? 'bg-white dark:bg-gray-600 text-gray-900 dark:text-gray-100 shadow-sm'
                      : 'text-gray-500 dark:text-gray-400 hover:text-gray-700 dark:hover:text-gray-200'
                  }`}
                >
                  {t('tasks.form.modeQuick')}
                </button>
                <button
                  onClick={() => setTaskMode('ai')}
                  className={`rounded-md px-3 py-1 text-xs font-medium transition-all ${
                    taskMode === 'ai'
                      ? 'bg-white dark:bg-gray-600 text-gray-900 dark:text-gray-100 shadow-sm'
                      : 'text-gray-500 dark:text-gray-400 hover:text-gray-700 dark:hover:text-gray-200'
                  }`}
                >
                  ✨ {t('tasks.form.modeAI')}
                </button>
              </div>
            </div>

            <div className="max-h-[80vh] overflow-y-auto p-6">
              <div key={taskMode} className="animate-tab-in">
                {taskMode === 'quick' ? (
                  <TaskForm
                    prefill={nlPrefill}
                    lists={lists}
                    onSubmit={handleCreateTask}
                    onCancel={closeTaskModal}
                    isLoading={createTask.isPending}
                    uploadProgress={uploadProgress}
                  />
                ) : (
                  <NaturalLanguageInput
                    lists={lists}
                    onParsed={(prefill) => {
                      setNlPrefill(prefill)
                      setTaskMode('quick')
                    }}
                    onCancel={closeTaskModal}
                  />
                )}
              </div>
            </div>
          </div>
        </Modal>
      )}
    </div>
    </>
  )
}
