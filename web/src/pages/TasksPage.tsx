import { useCallback, useEffect, useMemo, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import type { Task } from '@/types'
import { SYSTEM_LISTS, Status } from '@/types'
import type { ListTasksFilter } from '@/api/tasks'
import { TaskList } from '@/components/tasks/TaskList'
import { TaskForm, type TaskPrefill } from '@/components/tasks/TaskForm'
import { SubtaskList } from '@/components/tasks/SubtaskList'
import { DependencyPicker } from '@/components/tasks/DependencyPicker'
import { NaturalLanguageInput } from '@/components/tasks/NaturalLanguageInput'
import { DailyPlanPanel } from '@/components/tasks/DailyPlanPanel'
import { ErrorMessage } from '@/components/ui/ErrorMessage'
import { Modal } from '@/components/ui/Modal'
import { ToastContainer, type ToastMessage } from '@/components/ui/Toast'
import { useCreateTask, useUpdateTask } from '@/hooks/useTaskMutations'
import { useLists } from '@/hooks/useLists'
import { useNotifications, type TaskNotification } from '@/hooks/useNotifications'
import axios from 'axios'

type TaskMode = 'quick' | 'ai'
type ModalState = { type: 'create'; prefill?: TaskPrefill } | { type: 'edit'; task: Task } | null

const SYSTEM_IDS = new Set<string>(Object.values(SYSTEM_LISTS).map((s) => s.id))

function filterForListId(listId: string): ListTasksFilter {
  switch (listId) {
    case 'all':       return {}
    case 'today':     return { due_today: true }
    case 'scheduled': return { has_due_date: true }
    case 'flagged':   return { flagged_only: true }
    case 'completed': return { status: Status.DONE }
    case 'deleted':   return { include_deleted: true }
    default:          return { list_id: listId }
  }
}

export function TasksPage() {
  const { t } = useTranslation()
  const { listId = 'all' } = useParams<{ listId: string }>()
  const navigate = useNavigate()

  const [modal, setModal] = useState<ModalState>(null)
  const [taskMode, setTaskMode] = useState<TaskMode>('quick')
  const [nlPrefill, setNlPrefill] = useState<TaskPrefill | undefined>()
  const [formError, setFormError] = useState('')
  const [toasts, setToasts] = useState<ToastMessage[]>([])
  const [searchQuery, setSearchQuery] = useState('')

  const { data: lists = [] } = useLists()

  const filter = useMemo(() => filterForListId(listId), [listId])
  const filterKey = JSON.stringify(filter)

  useEffect(() => { setSearchQuery('') }, [listId])
  const isDeletedView = listId === 'deleted'
  const isCustomList = !SYSTEM_IDS.has(listId)

  const systemList = Object.values(SYSTEM_LISTS).find((s) => s.id === listId)
  const customList = lists.find((l) => l.id === listId)
  const listName = systemList ? t(systemList.nameKey) : (customList?.name ?? t('common.unknown'))
  const listColor = systemList?.color ?? customList?.color ?? '#007AFF'

  const dismissToast = useCallback((id: number) => {
    setToasts((prev) => prev.filter((t) => t.id !== id))
  }, [])

  const handleNotification = useCallback((n: TaskNotification) => {
    let text: string
    if (n.type === 'daily_plan_ready') {
      text = t('plan.ready')
    } else if (n.event_type === 'task.completed') {
      text = t('tasks.completed', { title: n.task_title ?? '' })
    } else {
      return
    }
    setToasts((prev) => [...prev, { id: Date.now(), text }])
  }, [t])

  useNotifications(handleNotification)

  const createTask = useCreateTask()
  const updateTask = useUpdateTask()

  function openCreateModal() {
    setFormError('')
    setNlPrefill(undefined)
    setTaskMode('quick')
    setModal({ type: 'create' })
  }

  function closeModal() {
    setModal(null)
    setTaskMode('quick')
    setNlPrefill(undefined)
    setFormError('')
  }

  function handleEdit(task: Task) {
    setFormError('')
    setModal({ type: 'edit', task })
  }

  async function handleCreate(values: { title: string; description: string; priority: number; status?: number; tags: string[]; due_at?: string | null; list_id?: string | null; flagged?: boolean; subtasks?: string[] }) {
    setFormError('')
    try {
      const { subtasks = [], list_id, due_at, ...rest } = values
      const newTask = await createTask.mutateAsync({
        ...rest,
        due_at: due_at ?? undefined,
        list_id: list_id ?? undefined,
      })
      if (subtasks.length > 0) {
        await Promise.all(subtasks.map((title) =>
          createTask.mutateAsync({ title, parent_task_id: newTask.id, list_id: newTask.list_id ?? undefined })
        ))
      }
      closeModal()
    } catch (err) {
      if (axios.isAxiosError(err) && err.response?.data?.error) {
        setFormError(err.response.data.error as string)
      } else {
        setFormError(t('tasks.failedToCreate'))
      }
    }
  }

  async function handleUpdate(values: { title: string; description: string; priority: number; status?: number; tags: string[]; due_at?: string | null; list_id?: string | null; flagged?: boolean }) {
    if (modal?.type !== 'edit') return
    setFormError('')
    try {
      await updateTask.mutateAsync({ id: modal.task.id, input: values })
      closeModal()
    } catch (err) {
      if (axios.isAxiosError(err) && err.response?.data?.error) {
        setFormError(err.response.data.error as string)
      } else {
        setFormError(t('tasks.failedToUpdate'))
      }
    }
  }

  return (
    <>
      <ToastContainer toasts={toasts} onDismiss={dismissToast} />

      {/* Header — back + list identity only; creation is via FAB */}
      <div className="mb-6 flex items-center gap-3">
        <button
          onClick={() => navigate('/')}
          className="rounded-lg p-1 text-gray-400 hover:text-gray-600 dark:text-gray-500 dark:hover:text-gray-300"
          aria-label={t('tasks.backToLists')}
        >
          ‹
        </button>
        <div
          className="h-5 w-5 shrink-0 rounded-full"
          style={{ backgroundColor: listColor }}
        />
        <h1 className="flex-1 text-2xl font-bold text-gray-900 dark:text-gray-100">{listName}</h1>
      </div>

      {listId === 'all' && <DailyPlanPanel />}

      {/* Filter bar */}
      <div className="mb-4 flex items-center gap-2 rounded-xl bg-white dark:bg-gray-800 px-4 py-2.5 shadow-sm ring-1 ring-transparent focus-within:ring-indigo-400/60 transition-shadow">
        <svg className="h-4 w-4 shrink-0 text-gray-400" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
          <circle cx="11" cy="11" r="8"/>
          <path d="m21 21-4.35-4.35"/>
        </svg>
        <input
          value={searchQuery}
          onChange={(e) => setSearchQuery(e.target.value)}
          placeholder={t('search.filterPlaceholder')}
          className="flex-1 min-w-0 bg-transparent text-sm outline-none placeholder:text-gray-400 dark:placeholder:text-gray-500 text-gray-900 dark:text-gray-100"
        />
        {searchQuery && (
          <button
            onClick={() => setSearchQuery('')}
            className="shrink-0 text-gray-400 hover:text-gray-600 dark:hover:text-gray-300"
          >
            <svg className="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
              <path d="M18 6 6 18M6 6l12 12"/>
            </svg>
          </button>
        )}
      </div>

      <div className="pb-24">
        <TaskList
          filter={filter}
          filterKey={filterKey}
          onEdit={handleEdit}
          isDeletedView={isDeletedView}
          searchQuery={searchQuery}
        />
      </div>

      {/* FAB — new task (hidden on deleted view) */}
      {!isDeletedView && (
        <button
          onClick={openCreateModal}
          aria-label={t('lists.home.newTask')}
          className="fixed bottom-6 right-6 z-40 flex h-14 w-14 items-center justify-center rounded-full bg-indigo-600 dark:bg-indigo-500 text-white shadow-lg hover:bg-indigo-700 dark:hover:bg-indigo-600 active:scale-95 transition-all"
        >
          <svg className="h-7 w-7" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2.5}>
            <path strokeLinecap="round" strokeLinejoin="round" d="M12 4.5v15m7.5-7.5h-15" />
          </svg>
        </button>
      )}

      {/* Task modal (create + edit) */}
      {modal && (
        <Modal onClose={closeModal} className="max-w-xl">
          <div className="rounded-xl bg-white dark:bg-gray-800 shadow-xl overflow-hidden">
            {modal.type === 'edit' ? (
              /* Edit modal — no tabs */
              <div className="max-h-[90vh] overflow-y-auto p-6">
                <h2 className="mb-4 text-lg font-semibold text-gray-900 dark:text-gray-100">
                  {t('tasks.form.editTask')}
                </h2>
                {formError && <div className="mb-4"><ErrorMessage message={formError} /></div>}
                <TaskForm
                  task={modal.task}
                  lists={lists}
                  defaultListId={isCustomList ? listId : undefined}
                  onSubmit={handleUpdate}
                  onCancel={closeModal}
                  isLoading={updateTask.isPending}
                >
                  <div className="border-t border-gray-100 dark:border-gray-700 pt-4">
                    <p className="mb-2 text-sm font-medium text-gray-700 dark:text-gray-300">{t('tasks.form.subtasksLabel')}</p>
                    <SubtaskList parentTask={modal.task} />
                  </div>
                  <DependencyPicker task={modal.task} />
                </TaskForm>
              </div>
            ) : (
              /* Create modal — Quick / AI Parse tabs */
              <>
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
                        defaultListId={isCustomList ? listId : undefined}
                        onSubmit={handleCreate}
                        onCancel={closeModal}
                        isLoading={createTask.isPending}
                      />
                    ) : (
                      <NaturalLanguageInput
                        lists={lists}
                        onParsed={(prefill) => {
                          setNlPrefill(prefill)
                          setTaskMode('quick')
                        }}
                        onCancel={closeModal}
                      />
                    )}
                    {formError && taskMode === 'quick' && (
                      <div className="mt-3"><ErrorMessage message={formError} /></div>
                    )}
                  </div>
                </div>
              </>
            )}
          </div>
        </Modal>
      )}
    </>
  )
}
