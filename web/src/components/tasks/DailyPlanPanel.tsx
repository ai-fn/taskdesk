import { usePlan } from '@/hooks/usePlan'
import { Spinner } from '@/components/ui/Spinner'

export function DailyPlanPanel() {
  const { data, isLoading, isError } = usePlan()

  if (isLoading) {
    return (
      <div className="mb-6 rounded-xl border border-indigo-100 bg-indigo-50 p-4">
        <div className="flex items-center gap-2 text-indigo-700">
          <Spinner />
          <span className="text-sm">Loading today's plan…</span>
        </div>
      </div>
    )
  }

  if (isError || !data?.plan) {
    return null
  }

  const { plan } = data
  const date = new Date(plan.generated_at).toLocaleDateString(undefined, {
    weekday: 'long', month: 'long', day: 'numeric',
  })

  return (
    <div className="mb-6 rounded-xl border border-indigo-200 bg-indigo-50 p-4">
      <div className="mb-2 flex items-center justify-between">
        <h2 className="font-semibold text-indigo-900">AI Daily Plan</h2>
        <span className="text-xs text-indigo-500">{date}</span>
      </div>
      <p className="mb-3 text-sm text-indigo-800">{plan.summary}</p>
      <ol className="space-y-1">
        {plan.items.slice(0, 5).map((item) => (
          <li key={item.task_id} className="flex items-start gap-2 text-sm">
            <span className="mt-0.5 flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-indigo-600 text-xs font-bold text-white">
              {item.rank}
            </span>
            <div>
              <span className="font-medium text-gray-900">{item.task_title}</span>
              <span className="ml-1 text-gray-500">— {item.rationale}</span>
            </div>
          </li>
        ))}
        {plan.items.length > 5 && (
          <li className="text-xs text-indigo-500 pl-7">
            +{plan.items.length - 5} more tasks in the plan
          </li>
        )}
      </ol>
    </div>
  )
}
