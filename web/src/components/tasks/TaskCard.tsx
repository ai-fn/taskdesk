import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import type { Task } from '@/types'
import { Priority, Status, PriorityLabelKey, StatusLabelKey } from '@/types'
import { Button } from '@/components/ui/Button'
import { useDeleteTask, useUpdateStatus, useToggleFlagged, useRestoreTask, usePurgeTask } from '@/hooks/useTaskMutations'
import { SubtaskList } from './SubtaskList'
import { toBCP47 } from '@/i18n/config'
import { useAuth } from '@/hooks/useAuth'

const priorityColors: Record<Priority, string> = {
  [Priority.UNSPECIFIED]: 'bg-gray-100 text-gray-600 dark:bg-gray-700 dark:text-gray-300',
  [Priority.LOW]: 'bg-blue-100 text-blue-700 dark:bg-blue-900/40 dark:text-blue-300',
  [Priority.MEDIUM]: 'bg-yellow-100 text-yellow-700 dark:bg-yellow-900/40 dark:text-yellow-300',
  [Priority.HIGH]: 'bg-orange-100 text-orange-700 dark:bg-orange-900/40 dark:text-orange-300',
  [Priority.URGENT]: 'bg-red-100 text-red-700 dark:bg-red-900/40 dark:text-red-300',
}

function scoreColor(score: number) {
  if (score >= 70) return 'bg-green-100 text-green-700 dark:bg-green-900/40 dark:text-green-300'
  if (score >= 40) return 'bg-yellow-100 text-yellow-700 dark:bg-yellow-900/40 dark:text-yellow-300'
  return 'bg-red-100 text-red-700 dark:bg-red-900/40 dark:text-red-300'
}

const statusSelectColors: Record<Status, string> = {
  [Status.UNSPECIFIED]: 'bg-gray-100 text-gray-600 dark:bg-gray-700 dark:text-gray-300',
  [Status.TODO]: 'bg-slate-100 text-slate-700 dark:bg-slate-700 dark:text-slate-200',
  [Status.IN_PROGRESS]: 'bg-purple-100 text-purple-700 dark:bg-purple-900/40 dark:text-purple-300',
  [Status.DONE]: 'bg-green-100 text-green-700 dark:bg-green-900/40 dark:text-green-300',
}

interface Props {
  task: Task
  onEdit: (task: Task) => void
  filterKey?: string
  isDeletedView?: boolean
}

export function TaskCard({ task, onEdit, filterKey, isDeletedView }: Props) {
  const { t, i18n } = useTranslation()
  const navigate = useNavigate()
  const { plan } = useAuth()
  const del = useDeleteTask(filterKey)
  const updateStatus = useUpdateStatus(filterKey)
  const toggleFlagged = useToggleFlagged(filterKey)
  const restore = useRestoreTask(filterKey)
  const purge = usePurgeTask(filterKey)

  const [subtasksOpen, setSubtasksOpen] = useState(false)

  const isDone = task.status === Status.DONE
  const isTemp = task.id.startsWith('temp-')
  const isStatusPending = updateStatus.isPending && updateStatus.variables?.id === task.id
  const subtaskCount = task.subtask_count ?? 0
  const subtaskDone = task.subtask_done_count ?? 0
  const subtaskPct = subtaskCount > 0 ? Math.round((subtaskDone / subtaskCount) * 100) : 0

  function openDetail() {
    if (isTemp || isDeletedView) return
    navigate(`/tasks/detail/${task.id}`)
  }

  const stop = (e: React.SyntheticEvent) => e.stopPropagation()

  return (
    <div
      onClick={openDetail}
      role={isTemp || isDeletedView ? undefined : 'button'}
      tabIndex={isTemp || isDeletedView ? undefined : 0}
      onKeyDown={(e) => { if (e.key === 'Enter') openDetail() }}
      className={`rounded-lg border border-gray-200 dark:border-gray-700 bg-white dark:bg-gray-800 p-4 shadow-sm transition-opacity ${isDone ? 'opacity-60' : ''} ${!isTemp && !isDeletedView ? 'cursor-pointer hover:border-indigo-300 dark:hover:border-indigo-600' : ''}`}
    >
      {/* ── Top row: content + compact icon buttons ── */}
      <div className="flex items-start gap-2 min-w-0">
        <div className="flex-1 min-w-0">
          {/* Title line */}
          <div className="flex items-center gap-1.5 min-w-0">
            <p className={`font-medium text-gray-900 dark:text-gray-100 truncate ${isDone ? 'line-through' : ''}`}>
              {task.title}
            </p>
            {task.flagged && <span className="shrink-0 text-red-500 text-sm">⚑</span>}
            {task.blocked && (
              <span
                title={t('tasks.detail.blockedByTitle')}
                className="shrink-0 text-amber-500 dark:text-amber-400 text-sm"
                aria-label={t('tasks.detail.blockedAriaLabel')}
              >
                🔒
              </span>
            )}
          </div>

          {task.description && (
            <p className="mt-1 text-sm text-gray-500 dark:text-gray-400 line-clamp-2">{task.description}</p>
          )}

          {/* Badges row */}
          <div className="mt-2 flex flex-wrap items-center gap-1.5">
            {task.priority !== Priority.UNSPECIFIED && (
              <span className={`shrink-0 whitespace-nowrap rounded-full px-2 py-0.5 text-xs font-medium ${priorityColors[task.priority]}`}>
                {t(PriorityLabelKey[task.priority])}
              </span>
            )}
            {isTemp ? (
              <span className={`shrink-0 whitespace-nowrap rounded-full px-2 py-0.5 text-xs font-medium ${statusSelectColors[task.status]}`}>
                {t('tasks.status.todo')}
              </span>
            ) : (
              <select
                value={task.status}
                disabled={isStatusPending || isDeletedView}
                onClick={stop}
                onMouseDown={stop}
                onChange={(e) => {
                  e.stopPropagation()
                  updateStatus.mutate({ id: task.id, status: Number(e.target.value) as Status })
                }}
                title={task.blocked ? t('tasks.detail.completeBlockedTitle') : undefined}
                className={`shrink-0 rounded-full px-2 py-0.5 text-xs font-medium cursor-pointer border-0 outline-none focus:ring-1 focus:ring-indigo-400 disabled:opacity-50 ${statusSelectColors[task.status]}`}
              >
                <option value={Status.TODO}>{t(StatusLabelKey[Status.TODO])}</option>
                <option value={Status.IN_PROGRESS}>{t(StatusLabelKey[Status.IN_PROGRESS])}</option>
                <option value={Status.DONE} disabled={task.blocked}>
                  {task.blocked ? t('tasks.card.statusBlocked') : t(StatusLabelKey[Status.DONE])}
                </option>
              </select>
            )}
            {task.tags?.map((tag) => (
              <span key={tag} className="shrink-0 whitespace-nowrap rounded-full bg-indigo-50 dark:bg-indigo-900/40 px-2 py-0.5 text-xs text-indigo-600 dark:text-indigo-300">
                {tag}
              </span>
            ))}
            {task.due_at && (
              <span className="shrink-0 whitespace-nowrap text-xs text-gray-400 dark:text-gray-500">
                {t('tasks.card.due', { date: new Date(task.due_at).toLocaleDateString(toBCP47(i18n.language)) })}
              </span>
            )}
            {plan === 'pro' && task.completion_score != null && task.completion_score > 0 && (
              <span
                className={`shrink-0 whitespace-nowrap rounded-full px-2 py-0.5 text-xs font-medium cursor-default ${scoreColor(task.completion_score)}`}
                title={task.completion_insight || undefined}
              >
                ✦ {task.completion_score}%
              </span>
            )}
          </div>

          {/* Subtask progress bar */}
          {!isTemp && subtaskCount > 0 && (
            <button
              onClick={(e) => { e.stopPropagation(); setSubtasksOpen((o) => !o) }}
              className="mt-2 flex items-center gap-2 text-xs text-gray-500 dark:text-gray-400 hover:text-gray-700 dark:hover:text-gray-200 transition-colors"
            >
              <svg
                viewBox="0 0 16 16"
                fill="none"
                stroke="currentColor"
                strokeWidth="2"
                className={`w-3 h-3 shrink-0 transition-transform ${subtasksOpen ? 'rotate-90' : ''}`}
              >
                <path d="M6 4l4 4-4 4"/>
              </svg>
              <span className="whitespace-nowrap">{t('tasks.card.subtasksDone', { done: subtaskDone, count: subtaskCount })}</span>
              <div className="w-16 h-1 rounded-full bg-gray-100 dark:bg-gray-700 overflow-hidden shrink-0">
                <div
                  className="h-full rounded-full bg-indigo-400 dark:bg-indigo-500 transition-all"
                  style={{ width: `${subtaskPct}%` }}
                />
              </div>
            </button>
          )}

          {!isTemp && subtasksOpen && (
            <div onClick={stop}>
              <SubtaskList parentTask={task} />
            </div>
          )}
        </div>

        {/* Icon-only buttons: flag + subtask toggle */}
        {!isTemp && !isDeletedView && (
          <div className="flex shrink-0 flex-col items-center gap-0.5 pt-0.5" onClick={stop}>
            <button
              onClick={() => toggleFlagged.mutate({ id: task.id, flagged: !task.flagged })}
              className={`rounded p-1 text-sm transition-colors ${
                task.flagged
                  ? 'text-red-500 hover:text-red-700 dark:text-red-400 dark:hover:text-red-300'
                  : 'text-gray-300 dark:text-gray-600 hover:text-red-400 dark:hover:text-red-500'
              }`}
              title={task.flagged ? t('tasks.unflag') : t('tasks.flag')}
            >
              ⚑
            </button>
            <button
              onClick={() => setSubtasksOpen((o) => !o)}
              className="rounded p-1 text-xs text-gray-300 dark:text-gray-600 hover:text-indigo-500 dark:hover:text-indigo-400 transition-colors"
              title={t('tasks.form.subtasksLabel')}
            >
              ⊕
            </button>
          </div>
        )}
      </div>

      {/* ── Footer action row ── */}
      {!isTemp && (
        <div
          className="mt-3 flex items-center justify-end gap-1 border-t border-gray-100 dark:border-gray-700 pt-2"
          onClick={stop}
        >
          {isDeletedView ? (
            <>
              <Button
                variant="ghost"
                className="text-xs text-green-600 hover:text-green-800 dark:text-green-400 dark:hover:text-green-300"
                loading={restore.isPending && restore.variables === task.id}
                onClick={() => restore.mutate(task.id)}
              >
                {t('tasks.card.restore')}
              </Button>
              <Button
                variant="ghost"
                className="text-xs text-red-500 hover:text-red-700 dark:text-red-400 dark:hover:text-red-300"
                loading={purge.isPending && purge.variables === task.id}
                onClick={() => {
                  if (confirm(t('tasks.card.confirmPurge'))) {
                    purge.mutate(task.id)
                  }
                }}
              >
                {t('common.delete')}
              </Button>
            </>
          ) : (
            <>
              <Button
                variant="ghost"
                className="text-xs"
                onClick={() => onEdit(task)}
              >
                {t('common.edit')}
              </Button>
              <Button
                variant="ghost"
                className="text-xs text-red-500 hover:text-red-700 dark:text-red-400 dark:hover:text-red-300"
                loading={del.isPending && del.variables === task.id}
                onClick={() => del.mutate(task.id)}
              >
                {t('common.delete')}
              </Button>
            </>
          )}
        </div>
      )}
    </div>
  )
}
