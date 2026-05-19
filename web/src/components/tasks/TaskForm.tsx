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

interface Props {
  task?: Task
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
  }) => void
  onCancel: () => void
  isLoading?: boolean
}

const fieldClass = 'rounded-md border border-gray-300 dark:border-gray-600 px-3 py-2 text-sm shadow-sm focus:border-indigo-500 focus:outline-none focus:ring-1 focus:ring-indigo-500 bg-white dark:bg-gray-800 text-gray-900 dark:text-gray-100'

export function TaskForm({ task, lists = [], defaultListId, onSubmit, onCancel, isLoading }: Props) {
  const {
    register,
    handleSubmit,
    formState: { errors },
  } = useForm<FormValues>({
    defaultValues: {
      title: task?.title ?? '',
      description: task?.description ?? '',
      priority: task?.priority ?? Priority.UNSPECIFIED,
      status: task?.status ?? Status.TODO,
      tags: task?.tags?.join(', ') ?? '',
      due_at: task?.due_at ? task.due_at.slice(0, 16) : '',
      list_id: task?.list_id ?? defaultListId ?? '',
      flagged: task?.flagged ?? false,
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
