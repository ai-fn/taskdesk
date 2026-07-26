import { useState, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import {
  DndContext,
  closestCenter,
  KeyboardSensor,
  PointerSensor,
  useSensor,
  useSensors,
  type DragEndEvent,
} from '@dnd-kit/core'
import {
  SortableContext,
  sortableKeyboardCoordinates,
  useSortable,
  verticalListSortingStrategy,
  arrayMove,
} from '@dnd-kit/sortable'
import { CSS } from '@dnd-kit/utilities'
import type { TaskList, ListGroup } from '@/types'
import { useDeleteList, useReorderLists, useUpdateList } from '@/hooks/useLists'
import { useCreateGroup, useUpdateGroup, useDeleteGroup } from '@/hooks/useGroups'
import { useEscapeKey } from '@/hooks/useEscapeKey'
import { ListFormModal } from './ListFormModal'

// ── Sortable list item ────────────────────────────────────────────────────────

interface SortableItemProps {
  list: TaskList
  groups: ListGroup[]
  onEdit: (list: TaskList) => void
  onDelete: (id: string) => void
  onToggleHide: (list: TaskList) => void
  onMoveToGroup: (list: TaskList, groupId: string) => void
}

function SortableItem({ list, groups, onEdit, onDelete, onToggleHide, onMoveToGroup }: SortableItemProps) {
  const { t } = useTranslation()
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({ id: list.id })
  const style = {
    transform: CSS.Transform.toString(transform),
    transition,
    opacity: isDragging ? 0.5 : 1,
  }

  return (
    <div ref={setNodeRef} style={style} className="flex items-center gap-3 rounded-lg bg-white dark:bg-gray-800 px-3 py-2.5 shadow-sm dark:shadow-gray-900/20">
      <button
        {...attributes}
        {...listeners}
        className="cursor-grab text-gray-400 hover:text-gray-600 dark:text-gray-500 dark:hover:text-gray-300 active:cursor-grabbing"
        aria-label={t('editLists.dragToReorder')}
      >
        ⠿
      </button>
      <div className="h-3 w-3 flex-shrink-0 rounded-full" style={{ backgroundColor: list.color }} />
      <span className="flex-1 truncate text-sm font-medium text-gray-800 dark:text-gray-200">{list.name}</span>
      <span className="text-xs text-gray-400 dark:text-gray-500">{list.task_count}</span>
      {groups.length > 1 && (
        <select
          value={list.group_id ?? ''}
          onChange={(e) => onMoveToGroup(list, e.target.value)}
          className="max-w-[90px] truncate rounded border border-gray-200 dark:border-gray-600 px-1 py-0.5 text-xs text-gray-500 dark:text-gray-400 bg-white dark:bg-gray-700"
          onClick={(e) => e.stopPropagation()}
        >
          <option value="">{t('editLists.noGroup')}</option>
          {groups.map((g) => (
            <option key={g.id} value={g.id}>{g.name}</option>
          ))}
        </select>
      )}
      <button onClick={() => onToggleHide(list)} className="text-gray-400 hover:text-gray-600 dark:text-gray-500 dark:hover:text-gray-300" title={list.is_hidden ? t('editLists.show') : t('editLists.hide')}>
        {list.is_hidden ? '👁' : '🙈'}
      </button>
      <button onClick={() => onEdit(list)} className="text-gray-400 hover:text-indigo-500 dark:text-gray-500 dark:hover:text-indigo-400" title={t('editLists.edit')}>✎</button>
      <button onClick={() => onDelete(list.id)} className="text-gray-400 hover:text-red-500 dark:text-gray-500 dark:hover:text-red-400" title={t('editLists.delete')}>✕</button>
    </div>
  )
}

// ── Group section header ──────────────────────────────────────────────────────

interface GroupHeaderProps {
  group: ListGroup
  onRename: (group: ListGroup) => void
  onDelete: (group: ListGroup) => void
}

function GroupHeader({ group, onRename, onDelete }: GroupHeaderProps) {
  const { t } = useTranslation()
  return (
    <div className="mt-4 mb-1 flex items-center gap-2">
      <span className="flex-1 text-xs font-semibold uppercase tracking-wide text-gray-400 dark:text-gray-500">{group.name}</span>
      <button onClick={() => onRename(group)} className="text-gray-400 hover:text-indigo-500 dark:text-gray-500 dark:hover:text-indigo-400 text-xs" title={t('editLists.renameGroup')}>✎</button>
      <button
        onClick={() => onDelete(group)}
        className="text-gray-400 hover:text-red-500 dark:text-gray-500 dark:hover:text-red-400 text-xs"
        title={t('editLists.deleteGroup')}
      >✕</button>
    </div>
  )
}

// ── Panel ─────────────────────────────────────────────────────────────────────

interface Props {
  lists: TaskList[]
  groups: ListGroup[]
  onClose: () => void
  onCreateNew: () => void
}

export function EditListsPanel({ lists, groups, onClose, onCreateNew }: Props) {
  const { t } = useTranslation()
  const [items, setItems] = useState(lists.map((l) => l.id))
  const [editingList, setEditingList] = useState<TaskList | null>(null)
  const [renamingGroup, setRenamingGroup] = useState<ListGroup | null>(null)
  const [newGroupName, setNewGroupName] = useState('')
  const [showNewGroup, setShowNewGroup] = useState(false)

  const [isClosing, setIsClosing] = useState(false)
  const handleClose = useCallback(() => {
    if (isClosing) return
    setIsClosing(true)
    setTimeout(onClose, 260)
  }, [isClosing, onClose])

  useEscapeKey(handleClose)

  const deleteList = useDeleteList()
  const reorder = useReorderLists()
  const updateList = useUpdateList()
  const createGroup = useCreateGroup()
  const updateGroup = useUpdateGroup()
  const deleteGroup = useDeleteGroup()

  const sensors = useSensors(
    useSensor(PointerSensor),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates })
  )

  function handleDragEnd(event: DragEndEvent) {
    const { active, over } = event
    if (!over || active.id === over.id) return
    const oldIndex = items.indexOf(active.id as string)
    const newIndex = items.indexOf(over.id as string)
    const newOrder = arrayMove(items, oldIndex, newIndex)
    setItems(newOrder)
    reorder.mutate(newOrder)
  }

  function handleDelete(id: string) {
    if (!confirm('Delete this list? Tasks in it will become unassigned.')) return
    deleteList.mutate(id)
    setItems((prev) => prev.filter((i) => i !== id))
  }

  function handleToggleHide(list: TaskList) {
    updateList.mutate({ id: list.id, input: { is_hidden: !list.is_hidden } })
  }

  function handleMoveToGroup(list: TaskList, groupId: string) {
    updateList.mutate({ id: list.id, input: { group_id: groupId || null } })
  }

  function handleDeleteGroup(group: ListGroup) {
    if (!confirm(`Delete group "${group.name}"? Lists in it will become ungrouped.`)) return
    deleteGroup.mutate(group.id)
  }

  function handleRenameGroup(group: ListGroup) {
    setRenamingGroup(group)
    setNewGroupName(group.name)
  }

  function submitRenameGroup() {
    if (!renamingGroup || !newGroupName.trim()) return
    updateGroup.mutate({ id: renamingGroup.id, name: newGroupName.trim() })
    setRenamingGroup(null)
  }

  function handleCreateGroup() {
    if (!newGroupName.trim()) return
    createGroup.mutate(newGroupName.trim())
    setNewGroupName('')
    setShowNewGroup(false)
  }

  const orderedLists = items
    .map((id) => lists.find((l) => l.id === id))
    .filter((l): l is TaskList => l != null)

  // Group lists by group_id; ungrouped go last under a null key
  const groupedLists = new Map<string | null, TaskList[]>()
  groupedLists.set(null, [])
  for (const g of groups) groupedLists.set(g.id, [])
  for (const l of orderedLists) {
    const key = l.group_id && groupedLists.has(l.group_id) ? l.group_id : null
    groupedLists.get(key)!.push(l)
  }

  const sections: Array<{ group: ListGroup | null; lists: TaskList[] }> = [
    ...groups.map((g) => ({ group: g, lists: groupedLists.get(g.id) ?? [] })),
    ...(groupedLists.get(null)!.length > 0 ? [{ group: null, lists: groupedLists.get(null)! }] : []),
  ]

  return (
    <>
      <div className={`fixed inset-0 z-40 bg-black/30 dark:bg-black/50 ${isClosing ? 'animate-backdrop-out' : 'animate-backdrop-in'}`} onClick={handleClose} />
      <div className={`fixed right-0 top-0 z-50 flex h-full w-full max-w-sm flex-col bg-gray-50 dark:bg-gray-900 shadow-2xl ${isClosing ? 'animate-slide-out-right' : 'animate-slide-in-right'}`}>
        <div className="flex items-center justify-between border-b border-gray-200 dark:border-gray-700 bg-white dark:bg-gray-800 px-4 py-3">
          <h2 className="text-base font-semibold text-gray-900 dark:text-gray-100">{t('editLists.title')}</h2>
          <button onClick={handleClose} className="text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200 text-xl">✕</button>
        </div>

        <div className="flex-1 overflow-y-auto p-4">
          <DndContext sensors={sensors} collisionDetection={closestCenter} onDragEnd={handleDragEnd}>
            <SortableContext items={items} strategy={verticalListSortingStrategy}>
              {sections.map(({ group, lists: sectionLists }) => (
                <div key={group?.id ?? '__ungrouped'}>
                  {group ? (
                    renamingGroup?.id === group.id ? (
                      <div className="mt-4 mb-1 flex items-center gap-2">
                        <input
                          className="flex-1 rounded border border-indigo-300 dark:border-indigo-600 px-2 py-0.5 text-xs font-semibold focus:outline-none bg-white dark:bg-gray-800 text-gray-900 dark:text-gray-100"
                          value={newGroupName}
                          onChange={(e) => setNewGroupName(e.target.value)}
                          onKeyDown={(e) => { if (e.key === 'Enter') submitRenameGroup(); if (e.key === 'Escape') setRenamingGroup(null) }}
                          autoFocus
                        />
                        <button onClick={submitRenameGroup} className="text-xs text-indigo-600 dark:text-indigo-400 font-medium">{t('common.save')}</button>
                        <button onClick={() => setRenamingGroup(null)} className="text-xs text-gray-400 dark:text-gray-500">{t('common.cancel')}</button>
                      </div>
                    ) : (
                      <GroupHeader group={group} onRename={handleRenameGroup} onDelete={handleDeleteGroup} />
                    )
                  ) : (
                    groups.length > 0 && (
                      <div className="mt-4 mb-1">
                        <span className="text-xs font-semibold uppercase tracking-wide text-gray-300 dark:text-gray-600">{t('editLists.noGroup')}</span>
                      </div>
                    )
                  )}
                  <div className="flex flex-col gap-2">
                    {sectionLists.map((list) => (
                      <SortableItem
                        key={list.id}
                        list={list}
                        groups={groups}
                        onEdit={setEditingList}
                        onDelete={handleDelete}
                        onToggleHide={handleToggleHide}
                        onMoveToGroup={handleMoveToGroup}
                      />
                    ))}
                    {sectionLists.length === 0 && (
                      <p className="pl-1 text-xs text-gray-300 dark:text-gray-600 italic">{t('tasks.list.empty')}</p>
                    )}
                  </div>
                </div>
              ))}
            </SortableContext>
          </DndContext>
        </div>

        <div className="border-t border-gray-200 dark:border-gray-700 bg-white dark:bg-gray-800 p-4 flex flex-col gap-2">
          {showNewGroup ? (
            <div className="flex gap-2">
              <input
                className="flex-1 rounded-lg border border-gray-300 dark:border-gray-600 px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500 bg-white dark:bg-gray-700 text-gray-900 dark:text-gray-100"
                placeholder={t('lists.form.namePlaceholder')}
                value={newGroupName}
                onChange={(e) => setNewGroupName(e.target.value)}
                onKeyDown={(e) => { if (e.key === 'Enter') handleCreateGroup(); if (e.key === 'Escape') setShowNewGroup(false) }}
                autoFocus
              />
              <button
                onClick={handleCreateGroup}
                className="rounded-lg bg-indigo-600 px-3 py-2 text-sm font-medium text-white hover:bg-indigo-700"
              >
                {t('common.create')}
              </button>
            </div>
          ) : (
            <button
              onClick={() => { setShowNewGroup(true); setNewGroupName('') }}
              className="w-full rounded-xl border border-dashed border-indigo-300 dark:border-indigo-700 px-4 py-2 text-sm font-medium text-indigo-500 dark:text-indigo-400 hover:border-indigo-400 dark:hover:border-indigo-500 hover:text-indigo-600 dark:hover:text-indigo-300"
            >
              {t('editLists.newGroup')}
            </button>
          )}
          <button
            onClick={onCreateNew}
            className="w-full rounded-xl bg-indigo-600 px-4 py-2.5 text-sm font-medium text-white hover:bg-indigo-700"
          >
            {t('editLists.newList')}
          </button>
        </div>
      </div>

      {editingList && (
        <ListFormModal
          list={editingList}
          groups={groups}
          onSave={(name, color, groupId, icon) => {
            updateList.mutate({ id: editingList.id, input: { name, color, group_id: groupId ?? null, icon } })
            setEditingList(null)
          }}
          onCancel={() => setEditingList(null)}
          isLoading={updateList.isPending}
        />
      )}
    </>
  )
}
