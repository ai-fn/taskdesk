import { useCallback, useMemo, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import type { Task } from '@/types'
import { SYSTEM_LISTS, Status } from '@/types'
import type { ListTasksFilter } from '@/api/tasks'
import { TaskList } from '@/components/tasks/TaskList'
import { TaskForm } from '@/components/tasks/TaskForm'
import { DailyPlanPanel } from '@/components/tasks/DailyPlanPanel'
import { Button } from '@/components/ui/Button'
import { ErrorMessage } from '@/components/ui/ErrorMessage'
import { ToastContainer, type ToastMessage } from '@/components/ui/Toast'
import { useCreateTask, useUpdateTask } from '@/hooks/useTaskMutations'
import { useLists } from '@/hooks/useLists'
import { useNotifications, type TaskNotification } from '@/hooks/useNotifications'
import axios from 'axios'

type Modal = { type: 'create' } | { type: 'edit'; task: Task } | null

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
  const { listId = 'all' } = useParams<{ listId: string }>()
  const navigate = useNavigate()

  const [modal, setModal] = useState<Modal>(null)
  const [formError, setFormError] = useState('')
  const [toasts, setToasts] = useState<ToastMessage[]>([])

  const { data: lists = [] } = useLists()

  const filter = useMemo(() => filterForListId(listId), [listId])
  const filterKey = JSON.stringify(filter)
  const isDeletedView = listId === 'deleted'
  const isCustomList = !SYSTEM_IDS.has(listId)

  const systemList = Object.values(SYSTEM_LISTS).find((s) => s.id === listId)
  const customList = lists.find((l) => l.id === listId)
  const listName = systemList?.name ?? customList?.name ?? 'Tasks'
  const listColor = systemList?.color ?? customList?.color ?? '#007AFF'

  const dismissToast = useCallback((id: number) => {
    setToasts((prev) => prev.filter((t) => t.id !== id))
  }, [])

  const handleNotification = useCallback((n: TaskNotification) => {
    let text: string
    if (n.type === 'daily_plan_ready') {
      text = 'Your AI daily plan is ready!'
    } else if (n.event_type === 'task.completed') {
      text = `✓ Completed: ${n.task_title ?? ''}`
    } else {
      return
    }
    setToasts((prev) => [...prev, { id: Date.now(), text }])
  }, [])

  useNotifications(handleNotification)

  const createTask = useCreateTask()
  const updateTask = useUpdateTask()

  function handleEdit(task: Task) {
    setFormError('')
    setModal({ type: 'edit', task })
  }

  async function handleCreate(values: { title: string; description: string; priority: number; status?: number; tags: string[]; due_at?: string; list_id?: string | null; flagged?: boolean }) {
    setFormError('')
    try {
      const { list_id: _l, ...rest } = values
      await createTask.mutateAsync({
        ...rest,
        list_id: isCustomList ? listId : undefined,
      })
      setModal(null)
    } catch (err) {
      if (axios.isAxiosError(err) && err.response?.data?.error) {
        setFormError(err.response.data.error as string)
      } else {
        setFormError('Failed to create task.')
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
        setFormError('Failed to update task.')
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
          aria-label="Back to lists"
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
          <Button onClick={() => { setFormError(''); setModal({ type: 'create' }) }}>
            + New task
          </Button>
        )}
      </div>

      {listId === 'all' && <DailyPlanPanel />}

      <TaskList
        filter={filter}
        filterKey={filterKey}
        onEdit={handleEdit}
        isDeletedView={isDeletedView}
      />

      {modal && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 dark:bg-black/60 px-4">
          <div className="w-full max-w-md rounded-xl bg-white dark:bg-gray-800 p-6 shadow-xl">
            <h2 className="mb-4 text-lg font-semibold text-gray-900 dark:text-gray-100">
              {modal.type === 'create' ? 'New task' : 'Edit task'}
            </h2>
            {formError && <div className="mb-4"><ErrorMessage message={formError} /></div>}
            <TaskForm
              task={modal.type === 'edit' ? modal.task : undefined}
              lists={lists}
              defaultListId={isCustomList ? listId : undefined}
              onSubmit={modal.type === 'create' ? handleCreate : handleUpdate}
              onCancel={() => setModal(null)}
              isLoading={createTask.isPending || updateTask.isPending}
            />
          </div>
        </div>
      )}
    </>
  )
}
