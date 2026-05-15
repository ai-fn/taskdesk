import { useCallback, useState } from 'react'
import type { Task } from '@/types'
import { TaskList } from '@/components/tasks/TaskList'
import { TaskForm } from '@/components/tasks/TaskForm'
import { DailyPlanPanel } from '@/components/tasks/DailyPlanPanel'
import { Button } from '@/components/ui/Button'
import { ErrorMessage } from '@/components/ui/ErrorMessage'
import { ToastContainer, type ToastMessage } from '@/components/ui/Toast'
import { useCreateTask, useUpdateTask } from '@/hooks/useTaskMutations'
import { useNotifications, type TaskNotification } from '@/hooks/useNotifications'
import axios from 'axios'

type Modal = { type: 'create' } | { type: 'edit'; task: Task } | null

export function TasksPage() {
  const [modal, setModal] = useState<Modal>(null)
  const [formError, setFormError] = useState('')
  const [toasts, setToasts] = useState<ToastMessage[]>([])

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

  async function handleCreate(values: Parameters<typeof createTask.mutate>[0]) {
    setFormError('')
    try {
      await createTask.mutateAsync(values)
      setModal(null)
    } catch (err) {
      if (axios.isAxiosError(err) && err.response?.data?.error) {
        setFormError(err.response.data.error as string)
      } else {
        setFormError('Failed to create task.')
      }
    }
  }

  async function handleUpdate(values: { title: string; description: string; priority: number; tags: string[]; due_at?: string }) {
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
      <div className="mb-6 flex items-center justify-between">
        <h1 className="text-2xl font-bold text-gray-900">My tasks</h1>
        <Button onClick={() => { setFormError(''); setModal({ type: 'create' }) }}>
          + New task
        </Button>
      </div>

      <DailyPlanPanel />
      <TaskList onEdit={handleEdit} />

      {modal && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 px-4">
          <div className="w-full max-w-md rounded-xl bg-white p-6 shadow-xl">
            <h2 className="mb-4 text-lg font-semibold text-gray-900">
              {modal.type === 'create' ? 'New task' : 'Edit task'}
            </h2>
            {formError && <div className="mb-4"><ErrorMessage message={formError} /></div>}
            <TaskForm
              task={modal.type === 'edit' ? modal.task : undefined}
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
