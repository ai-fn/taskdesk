interface Props {
  percent: number // 0–100
  className?: string
}

export function ProgressBar({ percent, className = '' }: Props) {
  return (
    <div className={`h-1 w-full rounded-full bg-gray-100 dark:bg-gray-700 overflow-hidden ${className}`}>
      <div
        className="h-full rounded-full bg-indigo-500 transition-all duration-200"
        style={{ width: `${Math.min(100, Math.max(0, percent))}%` }}
      />
    </div>
  )
}
