import { useRef, useState, type ReactNode } from 'react'
import { useForm } from 'react-hook-form'
import type { Task, TaskList } from '@/types'
import { Priority, Status } from '@/types'
import { Input } from '@/components/ui/Input'
import { Button } from '@/components/ui/Button'

interface FormValues {
  title: string
  description: string
  priority: number
  status: number
  tags: string
  due_at: string
  list_id: string
  flagged: boolean
}

export interface TaskPrefill {
  title?: string
  description?: string
  priority?: number
  due_at?: string
  list_id?: string
  tags?: string[]
  flagged?: boolean
  subtasks?: string[]
}

interface Props {
  task?: Task
  prefill?: TaskPrefill
  lists?: TaskList[]
  defaultListId?: string
  onSubmit: (values: {
    title: string
    description: string
    priority: number
    status?: number
    tags: string[]
    due_at?: string
    list_id?: string | null
    flagged?: boolean
    subtasks?: string[]
  }) => void
  onCancel: () => void
  isLoading?: boolean
  children?: ReactNode
}

const fieldClass = 'rounded-md border border-gray-300 dark:border-gray-600 px-3 py-2 text-sm shadow-sm focus:border-indigo-500 focus:outline-none focus:ring-1 focus:ring-indigo-500 bg-white dark:bg-gray-800 text-gray-900 dark:text-gray-100'

export function TaskForm({ task, prefill, lists = [], defaultListId, onSubmit, onCancel, isLoading, children }: Props) {
  const isCreate = !task
  const [draftSubtasks, setDraftSubtasks] = useState<string[]>(isCreate ? (prefill?.subtasks ?? []) : [])
  const [newSubtask, setNewSubtask] = useState('')
  const subtaskInputRef = useRef<HTMLInputElement>(null)

  function addSubtask() {
    const t = newSubtask.trim()
    if (!t) return
    setDraftSubtasks((cur) => [...cur, t])
    setNewSubtask('')
    subtaskInputRef.current?.focus()
  }
  function removeSubtask(i: number) {
    setDraftSubtasks((cur) => cur.filter((_, idx) => idx !== i))
  }

  const {
    register,
    handleSubmit,
    formState: { errors },
  } = useForm<FormValues>({
    defaultValues: {
      title: task?.title ?? prefill?.title ?? '',
      description: task?.description ?? prefill?.description ?? '',
      priority: task?.priority ?? prefill?.priority ?? Priority.UNSPECIFIED,
      status: task?.status ?? Status.TODO,
      tags: task?.tags?.join(', ') ?? prefill?.tags?.join(', ') ?? '',
      due_at: task?.due_at
        ? task.due_at.slice(0, 16)
        : prefill?.due_at
          ? prefill.due_at.slice(0, 16)
          : '',
      list_id: task?.list_id ?? prefill?.list_id ?? defaultListId ?? '',
      flagged: task?.flagged ?? prefill?.flagged ?? false,
    },
  })

  function submit(values: FormValues) {
    onSubmit({
      title: values.title,
      description: values.description,
      priority: Number(values.priority),
      status: task ? Number(values.status) : undefined,
      tags: values.tags ? values.tags.split(',').map((t) => t.trim()).filter(Boolean) : [],
      due_at: values.due_at ? new Date(values.due_at).toISOString() : undefined,
      list_id: values.list_id === '' ? (task?.list_id ? null : undefined) : values.list_id,
      flagged: values.flagged,
      subtasks: isCreate ? draftSubtasks : undefined,
    })
  }

  return (
    <form onSubmit={handleSubmit(submit)} className="flex flex-col gap-4">
      <Input
        label="Title"
        placeholder="Task title"
        error={errors.title?.message}
        {...register('title', { required: 'Title is required' })}
      />
      <div className="flex flex-col gap-1">
        <label className="text-sm font-medium text-gray-700 dark:text-gray-300">Description</label>
        <textarea
          className={`${fieldClass} resize-none`}
          rows={3}
          placeholder="Optional description"
          {...register('description')}
        />
      </div>
      <div className="flex flex-col gap-1">
        <label className="text-sm font-medium text-gray-700 dark:text-gray-300">Priority</label>
        <select className={fieldClass} {...register('priority')}>
          <option value={Priority.UNSPECIFIED}>None</option>
          <option value={Priority.LOW}>Low</option>
          <option value={Priority.MEDIUM}>Medium</option>
          <option value={Priority.HIGH}>High</option>
          <option value={Priority.URGENT}>Urgent</option>
        </select>
      </div>
      {task && (
        <div className="flex flex-col gap-1">
          <label className="text-sm font-medium text-gray-700 dark:text-gray-300">Status</label>
          <select className={fieldClass} {...register('status')}>
            <option value={Status.TODO}>To Do</option>
            <option value={Status.IN_PROGRESS}>In Progress</option>
            <option value={Status.DONE}>Done</option>
          </select>
        </div>
      )}
      {lists.length > 0 && (
        <div className="flex flex-col gap-1">
          <label className="text-sm font-medium text-gray-700 dark:text-gray-300">List</label>
          <select className={fieldClass} {...register('list_id')}>
            <option value="">No list</option>
            {lists.map((l) => (
              <option key={l.id} value={l.id}>{l.name}</option>
            ))}
          </select>
        </div>
      )}
      <div className="flex items-center gap-2">
        <input
          type="checkbox"
          id="flagged"
          className="h-4 w-4 rounded border-gray-300 dark:border-gray-600 text-red-500 focus:ring-red-400 dark:bg-gray-800"
          {...register('flagged')}
        />
        <label htmlFor="flagged" className="text-sm font-medium text-gray-700 dark:text-gray-300">
          ⚑ Flagged
        </label>
      </div>
      <Input
        label="Tags (comma-separated)"
        placeholder="work, personal, urgent"
        {...register('tags')}
      />
      <Input
        label="Due date"
        type="datetime-local"
        {...register('due_at')}
      />
      {isCreate && (
        <div className="border-t border-gray-100 dark:border-gray-700 pt-4">
          <p className="mb-2 text-sm font-medium text-gray-700 dark:text-gray-300">Subtasks</p>
          <div className="space-y-1">
            {draftSubtasks.map((st, i) => (
              <div key={i} className="flex items-center gap-2 group py-0.5">
                <span className="h-1.5 w-1.5 rounded-full bg-indigo-400 shrink-0" />
                <span className="flex-1 text-sm text-gray-700 dark:text-gray-300 truncate">{st}</span>
                <button
                  type="button"
                  onClick={() => removeSubtask(i)}
                  className="opacity-0 group-hover:opacity-100 shrink-0 text-gray-400 hover:text-red-500 dark:hover:text-red-400 transition-opacity"
                  title="Remove subtask"
                >
                  <svg viewBox="0 0 16 16" fill="none" className="w-3.5 h-3.5" stroke="currentColor" strokeWidth="2">
                    <path d="M4 4l8 8M12 4l-8 8" />
                  </svg>
                </button>
              </div>
            ))}
            <div className="flex items-center gap-2 pt-0.5">
              <span className="shrink-0 text-gray-300 dark:text-gray-600 text-xs">+</span>
              <input
                ref={subtaskInputRef}
                value={newSubtask}
                onChange={(e) => setNewSubtask(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') {
                    e.preventDefault()
                    addSubtask()
                  }
                  if (e.key === 'Escape') setNewSubtask('')
                }}
                placeholder="Add subtask…"
                className="flex-1 bg-transparent text-sm text-gray-700 dark:text-gray-300 placeholder:text-gray-400 dark:placeholder:text-gray-500 outline-none border-b border-transparent focus:border-gray-200 dark:focus:border-gray-600 py-0.5"
              />
              {newSubtask.trim() && (
                <button
                  type="button"
                  onClick={addSubtask}
                  className="shrink-0 text-xs text-indigo-600 dark:text-indigo-400 hover:text-indigo-800 dark:hover:text-indigo-300"
                >
                  Add
                </button>
              )}
            </div>
          </div>
        </div>
      )}
      {children}
      <div className="flex justify-end gap-2 pt-2">
        <Button type="button" variant="secondary" onClick={onCancel}>
          Cancel
        </Button>
        <Button type="submit" loading={isLoading}>
          {task ? 'Save changes' : 'Create task'}
        </Button>
      </div>
    </form>
  )
}
