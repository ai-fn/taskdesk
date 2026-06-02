import { useMemo, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import axios from 'axios'
import { getDependencyTree, getTask } from '@/api/tasks'
import { taskKeys } from '@/api/queryKeys'
import type { DependencyTreeNode } from '@/api/tasks'
import { useTranslation } from 'react-i18next'
import { Priority, PriorityLabelKey, Status, StatusLabelKey } from '@/types'
import { toBCP47 } from '@/i18n/config'
import { useLists } from '@/hooks/useLists'
import { useUpdateStatus, useToggleFlagged, useUpdateTask } from '@/hooks/useTaskMutations'
import { SubtaskList } from '@/components/tasks/SubtaskList'
import { DependencyPicker } from '@/components/tasks/DependencyPicker'
import { TaskForm } from '@/components/tasks/TaskForm'
import { Spinner } from '@/components/ui/Spinner'
import { Button } from '@/components/ui/Button'
import { Modal } from '@/components/ui/Modal'
import { ErrorMessage } from '@/components/ui/ErrorMessage'
import { useAuth } from '@/hooks/useAuth'
import { PaywallModal } from '@/components/ui/PaywallModal'

const priorityColors: Record<Priority, string> = {
  [Priority.UNSPECIFIED]: 'bg-gray-100 text-gray-600 dark:bg-gray-700 dark:text-gray-300',
  [Priority.LOW]: 'bg-blue-100 text-blue-700 dark:bg-blue-900/40 dark:text-blue-300',
  [Priority.MEDIUM]: 'bg-yellow-100 text-yellow-700 dark:bg-yellow-900/40 dark:text-yellow-300',
  [Priority.HIGH]: 'bg-orange-100 text-orange-700 dark:bg-orange-900/40 dark:text-orange-300',
  [Priority.URGENT]: 'bg-red-100 text-red-700 dark:bg-red-900/40 dark:text-red-300',
}

const statusColors: Record<Status, string> = {
  [Status.UNSPECIFIED]: 'bg-gray-100 text-gray-600 dark:bg-gray-700 dark:text-gray-300',
  [Status.TODO]: 'bg-slate-100 text-slate-700 dark:bg-slate-700 dark:text-slate-200',
  [Status.IN_PROGRESS]: 'bg-purple-100 text-purple-700 dark:bg-purple-900/40 dark:text-purple-300',
  [Status.DONE]: 'bg-green-100 text-green-700 dark:bg-green-900/40 dark:text-green-300',
}

function scoreColor(score: number) {
  if (score >= 70) return 'bg-green-100 text-green-700 dark:bg-green-900/40 dark:text-green-300'
  if (score >= 40) return 'bg-yellow-100 text-yellow-700 dark:bg-yellow-900/40 dark:text-yellow-300'
  return 'bg-red-100 text-red-700 dark:bg-red-900/40 dark:text-red-300'
}

export function TaskDetailPage() {
  const { t, i18n } = useTranslation()
  const { taskId = '' } = useParams<{ taskId: string }>()
  const navigate = useNavigate()
  const { data: lists = [] } = useLists()

  const { data: task, isLoading: taskLoading, isError: taskError } = useQuery({
    queryKey: taskKeys.detail(taskId),
    queryFn: () => getTask(taskId),
    enabled: !!taskId,
  })

  const { data: tree, isLoading: treeLoading } = useQuery({
    queryKey: taskKeys.dependencyTree(taskId),
    queryFn: () => getDependencyTree(taskId),
    enabled: !!taskId,
  })

  const { plan } = useAuth()
  const updateStatus = useUpdateStatus()
  const toggleFlagged = useToggleFlagged()
  const updateTask = useUpdateTask()

  const [editOpen, setEditOpen] = useState(false)
  const [editError, setEditError] = useState('')
  const [paywallOpen, setPaywallOpen] = useState(false)

  const list = useMemo(() => lists.find((l) => l.id === task?.list_id), [lists, task])

  async function handleSave(values: { title: string; description: string; priority: number; status?: number; tags: string[]; due_at?: string; list_id?: string | null; flagged?: boolean }) {
    if (!task) return
    setEditError('')
    try {
      await updateTask.mutateAsync({ id: task.id, input: values })
      setEditOpen(false)
    } catch (err) {
      if (axios.isAxiosError(err) && err.response?.data?.error) {
        setEditError(err.response.data.error as string)
      } else {
        setEditError(t('tasks.failedToUpdate'))
      }
    }
  }

  if (taskLoading) {
    return (
      <div className="flex items-center justify-center py-20">
        <Spinner className="h-6 w-6 text-indigo-500" />
      </div>
    )
  }
  if (taskError || !task) {
    return (
      <div className="text-center py-20 text-gray-500 dark:text-gray-400">
        <p>{t('tasks.notFound')}</p>
        <button onClick={() => navigate('/')} className="mt-4 text-indigo-600 dark:text-indigo-400 hover:underline">
          {t('tasks.backToLists')}
        </button>
      </div>
    )
  }

  const dependsOn = tree?.depends_on ?? []
  const blocking  = tree?.blocking  ?? []
  const dependsOnDone = dependsOn.filter((n) => n.task.status === Status.DONE).length
  const blockingDone  = blocking.filter((n) => n.task.status === Status.DONE).length

  return (
    <div className="max-w-3xl mx-auto pb-12">
      <button
        onClick={() => navigate(-1)}
        className="mb-4 inline-flex items-center gap-1 text-sm text-gray-500 dark:text-gray-400 hover:text-gray-700 dark:hover:text-gray-200"
      >
        {t('tasks.back')}
      </button>

      {/* Hero */}
      <div className="rounded-xl border border-gray-200 dark:border-gray-700 bg-white dark:bg-gray-800 p-6 shadow-sm">
        <div className="flex items-start gap-3">
          <div className="flex-1 min-w-0">
            <div className="flex items-start gap-2 min-w-0">
              <h1 className={`break-words text-xl font-semibold text-gray-900 dark:text-gray-100 ${task.status === Status.DONE ? 'line-through opacity-60' : ''}`}>
                {task.title}
              </h1>
              {task.blocked && (
                <span title={t('tasks.detail.blockedByTitle')} className="shrink-0 text-amber-500 dark:text-amber-400">🔒</span>
              )}
            </div>

            {task.description && (
              <p className="mt-2 text-sm text-gray-600 dark:text-gray-300 whitespace-pre-wrap">
                {task.description}
              </p>
            )}

            <div className="mt-4 flex flex-wrap items-center gap-2">
              <select
                value={task.status}
                onChange={(e) => updateStatus.mutate({ id: task.id, status: Number(e.target.value) as Status })}
                disabled={updateStatus.isPending}
                title={task.blocked ? t('tasks.detail.completeBlockedTitle') : undefined}
                className={`shrink-0 rounded-full px-3 py-1 text-xs font-medium cursor-pointer border-0 outline-none focus:ring-1 focus:ring-indigo-400 ${statusColors[task.status]}`}
              >
                <option value={Status.TODO}>{t(StatusLabelKey[Status.TODO])}</option>
                <option value={Status.IN_PROGRESS}>{t(StatusLabelKey[Status.IN_PROGRESS])}</option>
                <option value={Status.DONE} disabled={task.blocked}>
                  {task.blocked ? t('tasks.card.statusBlocked') : t(StatusLabelKey[Status.DONE])}
                </option>
              </select>

              {task.priority !== Priority.UNSPECIFIED && (
                <span className={`shrink-0 whitespace-nowrap rounded-full px-3 py-1 text-xs font-medium ${priorityColors[task.priority]}`}>
                  {t(PriorityLabelKey[task.priority])}
                </span>
              )}

              {task.due_at && (
                <span className="shrink-0 whitespace-nowrap text-xs text-gray-500 dark:text-gray-400">
                  {t('tasks.card.due', { date: new Date(task.due_at).toLocaleDateString(toBCP47(i18n.language)) })}
                </span>
              )}

              {list && (
                <span
                  className="shrink-0 flex items-center gap-1 whitespace-nowrap text-xs text-gray-500 dark:text-gray-400"
                  title={t('tasks.detail.inList', { name: list.name })}
                >
                  <span className="h-2 w-2 shrink-0 rounded-full" style={{ backgroundColor: list.color }} />
                  {list.name}
                </span>
              )}

              {plan === 'pro' && task.completion_score != null && task.completion_score > 0 && (
                <span
                  className={`shrink-0 whitespace-nowrap rounded-full px-3 py-1 text-xs font-medium cursor-default ${scoreColor(task.completion_score)}`}
                  title={task.completion_insight || undefined}
                >
                  ✦ {task.completion_score}%
                </span>
              )}
              {plan === 'free' && (
                <>
                  <button
                    onClick={() => setPaywallOpen(true)}
                    className="shrink-0 whitespace-nowrap rounded-full px-3 py-1 text-xs font-medium bg-gray-100 text-gray-500 dark:bg-gray-700 dark:text-gray-400 cursor-pointer hover:bg-indigo-50 dark:hover:bg-indigo-900/30 transition-colors"
                  >
                    🔒 {t('tasks.card.scoreProOnly')}
                  </button>
                  {paywallOpen && (
                    <PaywallModal featureName={t('tasks.card.scoreFeatureName')} onClose={() => setPaywallOpen(false)} />
                  )}
                </>
              )}

              {task.tags?.map((tag) => (
                <span key={tag} className="shrink-0 whitespace-nowrap rounded-full bg-indigo-50 dark:bg-indigo-900/40 px-2 py-0.5 text-xs text-indigo-600 dark:text-indigo-300">
                  {tag}
                </span>
              ))}
            </div>
          </div>

          <div className="flex shrink-0 items-start gap-2">
            <button
              onClick={() => toggleFlagged.mutate({ id: task.id, flagged: !task.flagged })}
              className={`rounded p-1 transition-colors ${
                task.flagged
                  ? 'text-red-500 hover:text-red-700 dark:text-red-400'
                  : 'text-gray-300 dark:text-gray-600 hover:text-red-400'
              }`}
              title={task.flagged ? t('tasks.unflag') : t('tasks.flag')}
            >
              ⚑
            </button>
            <Button
              variant="secondary"
              className="text-xs"
              onClick={() => { setEditError(''); setEditOpen(true) }}
            >
              {t('common.edit')}
            </Button>
          </div>
        </div>

        {plan === 'pro' && task.completion_insight && (
          <p className="mt-3 text-xs italic text-gray-500 dark:text-gray-400">
            ✦ {task.completion_insight}
          </p>
        )}
      </div>

      {/* Subtasks */}
      <section className="mt-6 rounded-xl border border-gray-200 dark:border-gray-700 bg-white dark:bg-gray-800 p-6 shadow-sm">
        <h2 className="mb-3 text-sm font-semibold text-gray-700 dark:text-gray-300 uppercase tracking-wide">{t('tasks.form.subtasksLabel')}</h2>
        <SubtaskList parentTask={task} />
      </section>

      {/* Blocked by */}
      <section className="mt-6 rounded-xl border border-gray-200 dark:border-gray-700 bg-white dark:bg-gray-800 p-6 shadow-sm">
        <div className="mb-3 flex items-baseline justify-between gap-3">
          <h2 className="text-sm font-semibold text-gray-700 dark:text-gray-300 uppercase tracking-wide">{t('tasks.detail.blockedBy')}</h2>
          {dependsOn.length > 0 && (
            <span className="text-xs text-gray-500 dark:text-gray-400">
              {t('tasks.detail.ofComplete', { done: dependsOnDone, total: dependsOn.length })}
            </span>
          )}
        </div>

        {treeLoading ? (
          <Spinner className="h-4 w-4 text-indigo-500" />
        ) : dependsOn.length === 0 ? (
          <p className="text-sm text-gray-400 dark:text-gray-500">{t('tasks.detail.noDependencies')}</p>
        ) : (
          <DependencyChain nodes={dependsOn} rootTitle={task.title} direction="up" />
        )}

        <div className="mt-4 border-t border-gray-100 dark:border-gray-700 pt-4">
          <DependencyPicker task={task} />
        </div>
      </section>

      {/* Blocking */}
      {blocking.length > 0 && (
        <section className="mt-6 rounded-xl border border-gray-200 dark:border-gray-700 bg-white dark:bg-gray-800 p-6 shadow-sm">
          <div className="mb-3 flex items-baseline justify-between gap-3">
            <h2 className="text-sm font-semibold text-gray-700 dark:text-gray-300 uppercase tracking-wide">{t('tasks.detail.blocks')}</h2>
            <span className="text-xs text-gray-500 dark:text-gray-400">
              {t('tasks.detail.ofComplete', { done: blockingDone, total: blocking.length })}
            </span>
          </div>
          <DependencyChain nodes={blocking} rootTitle={task.title} direction="down" />
        </section>
      )}

      {editOpen && (
        <Modal onClose={() => setEditOpen(false)} className="max-w-xl">
          <div className="rounded-xl bg-white dark:bg-gray-800 shadow-xl overflow-hidden">
            <div className="max-h-[90vh] overflow-y-auto p-6">
              <h2 className="mb-4 text-lg font-semibold text-gray-900 dark:text-gray-100">{t('tasks.form.editTask')}</h2>
              {editError && <div className="mb-4"><ErrorMessage message={editError} /></div>}
              <TaskForm
                task={task}
                lists={lists}
                onSubmit={handleSave}
                onCancel={() => setEditOpen(false)}
                isLoading={updateTask.isPending}
              />
            </div>
          </div>
        </Modal>
      )}
    </div>
  )
}

// ── DependencyChain ─────────────────────────────────────────────────────────
// Renders a flat-with-indentation tree from a flat node list keyed by `via`.

function DependencyChain({
  nodes,
  rootTitle,
  direction,
}: {
  nodes: DependencyTreeNode[]
  rootTitle: string
  direction: 'up' | 'down'
}) {
  // Group nodes by via_task_id so we can render a parent → child tree.
  const childrenByVia = useMemo(() => {
    const m = new Map<string, DependencyTreeNode[]>()
    for (const n of nodes) {
      const arr = m.get(n.via_task_id) ?? []
      arr.push(n)
      m.set(n.via_task_id, arr)
    }
    return m
  }, [nodes])

  // The "root" of this section is the task whose page we're on.
  // For 'up' direction: depth=1 nodes have via=rootTaskID
  // For 'down': same — via_task_id of depth=1 is the root task ID
  // We don't have rootTaskID directly here; instead, render every depth=1
  // node and recurse using each task's own ID as the via key.
  const directDeps = useMemo(() => nodes.filter((n) => n.depth === 1), [nodes])

  const arrow = direction === 'up' ? '↑' : '↓'

  return (
    <ul className="space-y-1.5">
      {directDeps.map((n) => (
        <DependencyChainNode
          key={n.task.id}
          node={n}
          childrenByVia={childrenByVia}
          rootArrow={arrow}
        />
      ))}
      <li className="ml-1 mt-2 text-xs text-gray-400 dark:text-gray-500">
        {arrow} {rootTitle}
      </li>
    </ul>
  )
}

function DependencyChainNode({
  node,
  childrenByVia,
  rootArrow,
}: {
  node: DependencyTreeNode
  childrenByVia: Map<string, DependencyTreeNode[]>
  rootArrow: string
}) {
  const { t } = useTranslation()
  const dep = node.task
  const isDone = dep.status === Status.DONE
  const children = childrenByVia.get(dep.id) ?? []

  return (
    <li>
      <div className="flex items-center gap-2 group py-0.5">
        <span
          className={`h-2 w-2 rounded-full shrink-0 ${
            isDone ? 'bg-green-500' : dep.status === Status.IN_PROGRESS ? 'bg-purple-500' : 'bg-amber-500'
          }`}
          title={isDone ? t(StatusLabelKey[Status.DONE]) : dep.status === Status.IN_PROGRESS ? t(StatusLabelKey[Status.IN_PROGRESS]) : t(StatusLabelKey[Status.TODO])}
        />
        <Link
          to={`/tasks/detail/${dep.id}`}
          className={`flex-1 text-sm truncate hover:underline ${
            isDone
              ? 'line-through text-gray-400 dark:text-gray-500'
              : 'text-gray-700 dark:text-gray-300'
          }`}
        >
          {dep.title}
        </Link>
        {dep.blocked && <span className="text-amber-500 text-xs" title={t('tasks.detail.blockedToo')}>🔒</span>}
      </div>
      {children.length > 0 && (
        <ul className="ml-4 mt-1 space-y-1 border-l-2 border-gray-100 dark:border-gray-700 pl-3">
          {children.map((c) => (
            <DependencyChainNode
              key={c.task.id}
              node={c}
              childrenByVia={childrenByVia}
              rootArrow={rootArrow}
            />
          ))}
        </ul>
      )}
    </li>
  )
}
