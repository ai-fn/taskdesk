import { useForm } from 'react-hook-form'
import type { Task } from '@/types'
import { Priority } from '@/types'
import { Input } from '@/components/ui/Input'
import { Button } from '@/components/ui/Button'

interface FormValues {
  title: string
  description: string
  priority: number
  tags: string
  due_at: string
}

interface Props {
  task?: Task
  onSubmit: (values: {
    title: string
    description: string
    priority: number
    tags: string[]
    due_at?: string
  }) => void
  onCancel: () => void
  isLoading?: boolean
}

export function TaskForm({ task, onSubmit, onCancel, isLoading }: Props) {
  const {
    register,
    handleSubmit,
    formState: { errors },
  } = useForm<FormValues>({
    defaultValues: {
      title: task?.title ?? '',
      description: task?.description ?? '',
      priority: task?.priority ?? Priority.UNSPECIFIED,
      tags: task?.tags?.join(', ') ?? '',
      due_at: task?.due_at ? task.due_at.slice(0, 16) : '', // trim to datetime-local format
    },
  })

  function submit(values: FormValues) {
    onSubmit({
      title: values.title,
      description: values.description,
      priority: Number(values.priority),
      tags: values.tags ? values.tags.split(',').map((t) => t.trim()).filter(Boolean) : [],
      due_at: values.due_at ? new Date(values.due_at).toISOString() : undefined,
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
        <label className="text-sm font-medium text-gray-700">Description</label>
        <textarea
          className="rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-indigo-500 focus:outline-none focus:ring-1 focus:ring-indigo-500"
          rows={3}
          placeholder="Optional description"
          {...register('description')}
        />
      </div>
      <div className="flex flex-col gap-1">
        <label className="text-sm font-medium text-gray-700">Priority</label>
        <select
          className="rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-indigo-500 focus:outline-none focus:ring-1 focus:ring-indigo-500"
          {...register('priority')}
        >
          <option value={Priority.UNSPECIFIED}>None</option>
          <option value={Priority.LOW}>Low</option>
          <option value={Priority.MEDIUM}>Medium</option>
          <option value={Priority.HIGH}>High</option>
          <option value={Priority.URGENT}>Urgent</option>
        </select>
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
