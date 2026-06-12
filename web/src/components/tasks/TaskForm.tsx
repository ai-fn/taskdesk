import { useRef, useState, type ReactNode, type DragEvent } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toBCP47 } from '@/i18n/config'
import type { Task, TaskList } from '@/types'
import { Priority, Status } from '@/types'
import { Input } from '@/components/ui/Input'
import { Button } from '@/components/ui/Button'
import { ProgressBar } from '@/components/ui/ProgressBar'
import { formatBytes } from '@/lib/format'
import { MULTIPART_THRESHOLD, FREE_MAX_FILE_BYTES } from '@/hooks/useAttachments'

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
    files?: File[]
  }) => void
  onCancel: () => void
  isLoading?: boolean
  /** Per-filename upload progress (0–100) injected by the parent during the upload phase. */
  uploadProgress?: Record<string, number>
  children?: ReactNode
}

const fieldClass = 'rounded-md border border-gray-300 dark:border-gray-600 px-3 py-2 text-sm shadow-sm focus:border-indigo-500 focus:outline-none focus:ring-1 focus:ring-indigo-500 bg-white dark:bg-gray-800 text-gray-900 dark:text-gray-100'

function fileIconFor(contentType: string): string {
  if (contentType.startsWith('image/')) return '🖼'
  if (contentType.startsWith('video/')) return '🎬'
  if (contentType.startsWith('audio/')) return '🎵'
  if (contentType.includes('pdf')) return '📄'
  if (contentType.includes('zip') || contentType.includes('tar') || contentType.includes('gzip')) return '🗜'
  if (contentType.startsWith('text/')) return '📝'
  return '📎'
}

export function TaskForm({ task, prefill, lists = [], defaultListId, onSubmit, onCancel, isLoading, uploadProgress, children }: Props) {
  const { t, i18n } = useTranslation()
  const isCreate = !task
  const locale = toBCP47(i18n.language)

  const [draftSubtasks, setDraftSubtasks] = useState<string[]>(isCreate ? (prefill?.subtasks ?? []) : [])
  const [newSubtask, setNewSubtask] = useState('')
  const subtaskInputRef = useRef<HTMLInputElement>(null)

  // Attachment draft state (create mode only)
  const [draftFiles, setDraftFiles] = useState<File[]>([])
  const [attachOpen, setAttachOpen] = useState(false)
  const [dragOver, setDragOver] = useState(false)
  const fileInputRef = useRef<HTMLInputElement>(null)

  const isUploading = uploadProgress !== undefined && Object.keys(uploadProgress).length > 0

  function addSubtask() {
    const val = newSubtask.trim()
    if (!val) return
    setDraftSubtasks((cur) => [...cur, val])
    setNewSubtask('')
    subtaskInputRef.current?.focus()
  }
  function removeSubtask(i: number) {
    setDraftSubtasks((cur) => cur.filter((_, idx) => idx !== i))
  }

  function queueFiles(files: FileList | File[]) {
    const next: File[] = []
    for (const file of Array.from(files)) {
      if (file.size > FREE_MAX_FILE_BYTES && !isCreate) continue
      // Files ≥ multipart threshold must be attached after creation (no multipart in create flow)
      if (file.size >= MULTIPART_THRESHOLD) continue
      // Avoid duplicates by name+size
      if (draftFiles.some((f) => f.name === file.name && f.size === file.size)) continue
      next.push(file)
    }
    if (next.length) setDraftFiles((prev) => [...prev, ...next])
  }

  function removeFile(idx: number) {
    setDraftFiles((prev) => prev.filter((_, i) => i !== idx))
  }

  function handleFileInput(e: React.ChangeEvent<HTMLInputElement>) {
    if (e.target.files) queueFiles(e.target.files)
    e.target.value = ''
  }

  function handleDrop(e: DragEvent<HTMLDivElement>) {
    e.preventDefault()
    setDragOver(false)
    if (e.dataTransfer.files) queueFiles(e.dataTransfer.files)
  }
  function handleDragOver(e: DragEvent<HTMLDivElement>) {
    e.preventDefault()
    setDragOver(true)
  }
  function handleDragLeave() {
    setDragOver(false)
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
      tags: values.tags ? values.tags.split(',').map((v) => v.trim()).filter(Boolean) : [],
      due_at: values.due_at ? new Date(values.due_at).toISOString() : undefined,
      list_id: values.list_id === '' ? (task?.list_id ? null : undefined) : values.list_id,
      flagged: values.flagged,
      subtasks: isCreate ? draftSubtasks : undefined,
      files: isCreate ? draftFiles : undefined,
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

      {/* ── Subtasks (create only) ── */}
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

      {/* ── Attachments (create only) ── */}
      {isCreate && (
        <div className="border-t border-gray-100 dark:border-gray-700 pt-4">
          {/* Hidden file input */}
          <input
            ref={fileInputRef}
            type="file"
            multiple
            className="hidden"
            onChange={handleFileInput}
          />

          {/* Header row */}
          <button
            type="button"
            onClick={() => setAttachOpen((o) => !o)}
            className="flex w-full items-center gap-2 text-sm font-medium text-gray-700 dark:text-gray-300 hover:text-gray-900 dark:hover:text-gray-100 transition-colors"
          >
            <span className="text-base leading-none">📎</span>
            <span className="flex-1 text-left">
              {draftFiles.length > 0
                ? t('attachments.attachedCount', { count: draftFiles.length })
                : t('attachments.attachFiles')}
            </span>
            <svg
              viewBox="0 0 16 16"
              fill="none"
              stroke="currentColor"
              strokeWidth="2"
              className={`w-3.5 h-3.5 shrink-0 transition-transform duration-150 ${attachOpen || draftFiles.length > 0 ? 'rotate-90' : ''}`}
            >
              <path d="M6 4l4 4-4 4" />
            </svg>
          </button>

          {/* Expandable drop zone + chip list */}
          {(attachOpen || draftFiles.length > 0) && (
            <div className="mt-2 space-y-1.5">
              {/* File chips */}
              {draftFiles.map((file, idx) => {
                const progress = uploadProgress?.[file.name]
                const isInProgress = progress !== undefined
                return (
                  <div
                    key={`${file.name}-${file.size}`}
                    className="flex items-center gap-2 rounded-md border border-gray-200 dark:border-gray-600 bg-gray-50 dark:bg-gray-700/50 px-2.5 py-1.5"
                    style={{ animation: 'chip-in 150ms ease-out both' }}
                  >
                    <span className="shrink-0 text-sm leading-none">{fileIconFor(file.type || 'application/octet-stream')}</span>
                    <div className="flex-1 min-w-0">
                      <p className="text-xs text-gray-700 dark:text-gray-300 truncate leading-tight">{file.name}</p>
                      {isInProgress ? (
                        <div className="mt-1 flex items-center gap-2">
                          <ProgressBar percent={progress} className="flex-1" />
                          <span className="text-xs text-gray-400 dark:text-gray-500 shrink-0 tabular-nums">{progress}%</span>
                        </div>
                      ) : (
                        <p className="text-xs text-gray-400 dark:text-gray-500 leading-tight">{formatBytes(file.size, locale)}</p>
                      )}
                    </div>
                    {!isUploading && (
                      <button
                        type="button"
                        onClick={() => removeFile(idx)}
                        className="shrink-0 text-gray-400 hover:text-red-500 dark:hover:text-red-400 transition-colors"
                        title={t('common.delete')}
                      >
                        <svg viewBox="0 0 16 16" fill="none" className="w-3 h-3" stroke="currentColor" strokeWidth="2.5">
                          <path d="M4 4l8 8M12 4l-8 8" />
                        </svg>
                      </button>
                    )}
                  </div>
                )
              })}

              {/* Upload progress label */}
              {isUploading && (
                <p className="text-xs text-indigo-600 dark:text-indigo-400 px-0.5">
                  {t('attachments.uploadingFiles')}
                </p>
              )}

              {/* Drop zone (only when not uploading) */}
              {!isUploading && (
                <div
                  onDrop={handleDrop}
                  onDragOver={handleDragOver}
                  onDragLeave={handleDragLeave}
                  onClick={() => fileInputRef.current?.click()}
                  className={`flex cursor-pointer items-center justify-center gap-2 rounded-md border border-dashed py-2.5 text-xs transition-colors ${
                    dragOver
                      ? 'border-indigo-400 bg-indigo-50 dark:border-indigo-500 dark:bg-indigo-900/20 text-indigo-600 dark:text-indigo-300'
                      : 'border-gray-200 dark:border-gray-600 text-gray-400 dark:text-gray-500 hover:border-indigo-300 dark:hover:border-indigo-600 hover:text-indigo-500 dark:hover:text-indigo-400'
                  }`}
                >
                  <span>📎</span>
                  <span>{t('attachments.dropHint')}</span>
                </div>
              )}
            </div>
          )}
        </div>
      )}

      {children}

      <div className="flex justify-end gap-2 pt-2">
        <Button type="button" variant="secondary" onClick={onCancel} disabled={isUploading}>
          {t('common.cancel')}
        </Button>
        <Button type="submit" loading={isLoading || isUploading}>
          {task ? t('tasks.form.saveChanges') : t('tasks.form.createTask')}
        </Button>
      </div>

      {/* Chip entrance animation */}
      <style>{`
        @keyframes chip-in {
          from { opacity: 0; transform: translateY(4px); }
          to   { opacity: 1; transform: translateY(0); }
        }
      `}</style>
    </form>
  )
}
