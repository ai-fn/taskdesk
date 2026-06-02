import { useNavigate } from 'react-router-dom'
import type { TaskList } from '@/types'

interface Props {
  list: TaskList
  href: string
}

export function ListCard({ list, href }: Props) {
  const navigate = useNavigate()

  return (
    <button
      onClick={() => navigate(href)}
      className="relative flex min-w-0 flex-col items-start gap-1 overflow-hidden rounded-xl bg-white dark:bg-gray-800 p-3 shadow-sm dark:shadow-gray-900/20 transition-all hover:shadow-md active:scale-95 w-full text-left"
    >
      <div
        className="flex h-9 w-9 shrink-0 items-center justify-center rounded-full text-white text-base font-semibold"
        style={{ backgroundColor: list.color }}
      >
        {list.icon ? list.icon : list.name.charAt(0).toUpperCase()}
      </div>
      <span className="mt-1 text-xl font-bold text-gray-900 dark:text-gray-100 tabular-nums">{list.task_count}</span>
      <span className="truncate w-full text-xs font-medium text-gray-600 dark:text-gray-400">{list.name}</span>
    </button>
  )
}

interface SystemCardProps {
  id: string
  name: string
  color: string
  icon: string
  count: number
  href: string
}

export function SystemListCard({ id: _id, name, color, icon, count, href }: SystemCardProps) {
  const navigate = useNavigate()

  return (
    <button
      onClick={() => navigate(href)}
      className="relative flex min-w-0 flex-col items-start gap-1 overflow-hidden rounded-xl bg-white dark:bg-gray-800 p-3 shadow-sm dark:shadow-gray-900/20 transition-all hover:shadow-md active:scale-95 w-full text-left"
    >
      <div
        className="flex h-9 w-9 shrink-0 items-center justify-center rounded-full text-white text-base"
        style={{ backgroundColor: color }}
      >
        {icon}
      </div>
      <span className="mt-1 text-xl font-bold text-gray-900 dark:text-gray-100 tabular-nums">{count}</span>
      <span className="truncate w-full text-xs font-medium text-gray-600 dark:text-gray-400">{name}</span>
    </button>
  )
}
