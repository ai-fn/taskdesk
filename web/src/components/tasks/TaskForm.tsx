import { useRef, useState, type ReactNode } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toBCP47 } from '@/i18n/config'
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
    due_at?: string | null // null = clear (edit only)
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
  const { t, i18n } = useTranslation()
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
      due_at: values.due_at ? new Date(values.due_at).toISOString() : (task?.due_at ? null : undefined),
      list_id: values.list_id === '' ? (task?.list_id ? null : undefined) : values.list_id,
      flagged: values.flagged,
      subtasks: isCreate ? draftSubtasks : undefined,
    })
  }

  return (
    <form onSubmit={handleSubmit(submit)} className="flex flex-col gap-4">
      <Input
        label={t('tasks.form.titleLabel')}
        placeholder={t('tasks.form.titlePlaceholder')}
        error={errors.title?.message}
        {...register('title', { required: t('tasks.form.titleLabel') })}
      />
      <div className="flex flex-col gap-1">
        <label className="text-sm font-medium text-gray-700 dark:text-gray-300">{t('tasks.form.descriptionLabel')}</label>
        <textarea
          className={`${fieldClass} resize-none`}
          rows={3}
          placeholder={t('tasks.form.descriptionPlaceholder')}
          {...register('description')}
        />
      </div>
      <div className="flex flex-col gap-1">
        <label className="text-sm font-medium text-gray-700 dark:text-gray-300">{t('tasks.form.priorityLabel')}</label>
        <select className={fieldClass} {...register('priority')}>
          <option value={Priority.UNSPECIFIED}>{t('tasks.priority.none')}</option>
          <option value={Priority.LOW}>{t('tasks.priority.low')}</option>
          <option value={Priority.MEDIUM}>{t('tasks.priority.medium')}</option>
          <option value={Priority.HIGH}>{t('tasks.priority.high')}</option>
          <option value={Priority.URGENT}>{t('tasks.priority.urgent')}</option>
        </select>
      </div>
      {task && (
        <div className="flex flex-col gap-1">
          <label className="text-sm font-medium text-gray-700 dark:text-gray-300">{t('tasks.form.statusLabel')}</label>
          <select
            className={fieldClass}
            title={task.blocked ? 'Complete blocking tasks before marking this Done' : undefined}
            {...register('status')}
          >
            <option value={Status.TODO}>{t('tasks.status.todo')}</option>
            <option value={Status.IN_PROGRESS}>{t('tasks.status.inProgress')}</option>
            <option value={Status.DONE} disabled={task.blocked}>
              {task.blocked ? t('tasks.card.statusBlocked') : t('tasks.status.done')}
            </option>
          </select>
        </div>
      )}
      {lists.length > 0 && (
        <div className="flex flex-col gap-1">
          <label className="text-sm font-medium text-gray-700 dark:text-gray-300">{t('tasks.form.listLabel')}</label>
          <select className={fieldClass} {...register('list_id')}>
            <option value="">{t('tasks.form.noList')}</option>
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
          {t('tasks.form.flaggedLabel')}
        </label>
      </div>
      <Input
        label={t('tasks.form.tagsLabel')}
        placeholder={t('tasks.form.tagsPlaceholder')}
        {...register('tags')}
      />
      <Input
        label={t('tasks.form.dueDateLabel')}
        type="datetime-local"
        lang={toBCP47(i18n.language)}
        {...register('due_at')}
      />
      {isCreate && (
        <div className="border-t border-gray-100 dark:border-gray-700 pt-4">
          <p className="mb-2 text-sm font-medium text-gray-700 dark:text-gray-300">{t('tasks.form.subtasksLabel')}</p>
          <div className="space-y-1">
            {draftSubtasks.map((st, i) => (
              <div key={i} className="flex items-center gap-2 group py-0.5">
                <span className="h-1.5 w-1.5 rounded-full bg-indigo-400 shrink-0" />
                <span className="flex-1 text-sm text-gray-700 dark:text-gray-300 truncate">{st}</span>
                <button
                  type="button"
                  onClick={() => removeSubtask(i)}
                  className="opacity-0 group-hover:opacity-100 shrink-0 text-gray-400 hover:text-red-500 dark:hover:text-red-400 transition-opacity"
                  title={t('tasks.subtasks.removeTitle')}
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
                placeholder={t('tasks.subtasks.addPlaceholder')}
                className="flex-1 bg-transparent text-sm text-gray-700 dark:text-gray-300 placeholder:text-gray-400 dark:placeholder:text-gray-500 outline-none border-b border-transparent focus:border-gray-200 dark:focus:border-gray-600 py-0.5"
              />
              {newSubtask.trim() && (
                <button
                  type="button"
                  onClick={addSubtask}
                  className="shrink-0 text-xs text-indigo-600 dark:text-indigo-400 hover:text-indigo-800 dark:hover:text-indigo-300"
                >
                  {t('common.add')}
                </button>
              )}
            </div>
          </div>
        </div>
      )}
      {children}
      <div className="flex justify-end gap-2 pt-2">
        <Button type="button" variant="secondary" onClick={onCancel}>
          {t('common.cancel')}
        </Button>
        <Button type="submit" loading={isLoading}>
          {task ? t('tasks.form.saveChanges') : t('tasks.form.createTask')}
        </Button>
      </div>
    </form>
  )
}
