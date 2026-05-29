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
import { Button } from '@/components/ui/Button'
import { ErrorMessage } from '@/components/ui/ErrorMessage'
import { Modal } from '@/components/ui/Modal'
import { ToastContainer, type ToastMessage } from '@/components/ui/Toast'
import { useCreateTask, useUpdateTask } from '@/hooks/useTaskMutations'
import { useLists } from '@/hooks/useLists'
import { useNotifications, type TaskNotification } from '@/hooks/useNotifications'
import axios from 'axios'

type Modal =
  | { type: 'create'; prefill?: TaskPrefill }
  | { type: 'nl' }
  | { type: 'edit'; task: Task }
  | null

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

  const [modal, setModal] = useState<Modal>(null)
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
  const listName = systemList ? t(systemList.nameKey) : (customList?.name ?? 'Tasks')
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

  function handleEdit(task: Task) {
    setFormError('')
    setModal({ type: 'edit', task })
  }

  async function handleCreate(values: { title: string; description: string; priority: number; status?: number; tags: string[]; due_at?: string; list_id?: string | null; flagged?: boolean; subtasks?: string[] }) {
    setFormError('')
    try {
      const { list_id: _l, subtasks = [], ...rest } = values
      const newTask = await createTask.mutateAsync({
        ...rest,
        list_id: isCustomList ? listId : undefined,
      })
      if (subtasks.length > 0) {
        await Promise.all(subtasks.map((title) =>
          createTask.mutateAsync({ title, parent_task_id: newTask.id, list_id: newTask.list_id ?? undefined })
        ))
      }
      setModal(null)
    } catch (err) {
      if (axios.isAxiosError(err) && err.response?.data?.error) {
        setFormError(err.response.data.error as string)
      } else {
        setFormError(t('tasks.failedToCreate'))
      }
    }
  }

  async function handleUpdate(values: { title: string; description: string; priority: number; status?: number; tags: string[]; due_at?: string; list_id?: string | null; flagged?: boolean }) {
    if (modal?.type !== 'edit') return
    setFormError('')
    try {
      await updateTask.mutateAsync({ id: modal.task.id, input: values })
      setModal(null)
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
      <div className="mb-6 flex items-center gap-3">
        <button
          onClick={() => navigate('/')}
          className="rounded-lg p-1 text-gray-400 hover:text-gray-600 dark:text-gray-500 dark:hover:text-gray-300"
          aria-label={t('tasks.backToLists')}
        >
          ‹
        </button>
        <div className="flex flex-1 items-center gap-2">
          <div
            className="h-5 w-5 rounded-full"
            style={{ backgroundColor: listColor }}
          />
          <h1 className="text-2xl font-bold text-gray-900 dark:text-gray-100">{listName}</h1>
        </div>
        {!isDeletedView && (
          <div className="flex items-center gap-2">
            <button
              onClick={() => { setFormError(''); setModal({ type: 'nl' }) }}
              className="rounded-lg p-1.5 text-gray-400 hover:text-indigo-500 dark:hover:text-indigo-400 transition-colors"
              title={t('tasks.parse.nlTooltip')}
            >
              ✨
            </button>
            <Button onClick={() => { setFormError(''); setModal({ type: 'create' }) }}>
              {t('lists.home.newTask')}
            </Button>
          </div>
        )}
      </div>

      {listId === 'all' && <DailyPlanPanel />}

      <div className="mb-4 flex items-center gap-2 rounded-xl bg-white dark:bg-gray-800 px-4 py-2.5 shadow-sm ring-1 ring-transparent focus-within:ring-indigo-400/60 transition-shadow">
        <svg className="h-4 w-4 shrink-0 text-gray-400" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
          <circle cx="11" cy="11" r="8"/>
          <path d="m21 21-4.35-4.35"/>
        </svg>
        <input
          value={searchQuery}
          onChange={(e) => setSearchQuery(e.target.value)}
          placeholder={t('search.filterPlaceholder')}
          className="flex-1 bg-transparent text-sm outline-none placeholder:text-gray-400 dark:placeholder:text-gray-500 text-gray-900 dark:text-gray-100"
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

      <TaskList
        filter={filter}
        filterKey={filterKey}
        onEdit={handleEdit}
        isDeletedView={isDeletedView}
        searchQuery={searchQuery}
      />

      {modal && (
        <Modal onClose={() => setModal(null)} className="max-w-xl">
          <div className="rounded-xl bg-white dark:bg-gray-800 shadow-xl overflow-hidden">
          <div className="max-h-[90vh] overflow-y-auto p-6">
            {modal.type === 'nl' ? (
              <>
                <h2 className="mb-4 text-lg font-semibold text-gray-900 dark:text-gray-100">
                  {t('tasks.form.newTask')}
                </h2>
                <NaturalLanguageInput
                  lists={lists}
                  onParsed={(prefill) => {
                    setFormError('')
                    setModal({ type: 'create', prefill })
                  }}
                  onCancel={() => setModal(null)}
                />
              </>
            ) : (
              <>
                <h2 className="mb-4 text-lg font-semibold text-gray-900 dark:text-gray-100">
                  {modal.type === 'create' ? t('tasks.form.newTask') : t('tasks.form.editTask')}
                </h2>
                {formError && <div className="mb-4"><ErrorMessage message={formError} /></div>}
                <TaskForm
                  task={modal.type === 'edit' ? modal.task : undefined}
                  prefill={modal.type === 'create' ? modal.prefill : undefined}
                  lists={lists}
                  defaultListId={isCustomList ? listId : undefined}
                  onSubmit={modal.type === 'create' ? handleCreate : handleUpdate}
                  onCancel={() => setModal(null)}
                  isLoading={createTask.isPending || updateTask.isPending}
                >
                  {modal.type === 'edit' && (
                    <>
                      <div className="border-t border-gray-100 dark:border-gray-700 pt-4">
                        <p className="mb-2 text-sm font-medium text-gray-700 dark:text-gray-300">{t('tasks.form.subtasksLabel')}</p>
                        <SubtaskList parentTask={modal.task} />
                      </div>
                      <DependencyPicker task={modal.task} />
                    </>
                  )}
                </TaskForm>
              </>
            )}
          </div>
          </div>
        </Modal>
      )}
    </>
  )
}
