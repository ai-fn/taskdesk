import { useState } from 'react'
import { usePlan, useGeneratePlan } from '@/hooks/usePlan'
import { Spinner } from '@/components/ui/Spinner'

export function DailyPlanPanel() {
  const { data, isLoading } = usePlan()
  const generate = useGeneratePlan()
  const [expanded, setExpanded] = useState(false)

  const plan = data?.plan

  return (
    <div className="mb-6 rounded-xl border border-indigo-200 bg-indigo-50 p-4">
      <div className="mb-2 flex items-center justify-between gap-2">
        <h2 className="font-semibold text-indigo-900">AI Daily Plan</h2>
        <div className="flex items-center gap-2">
          {generate.isPending && (
            <button
              onClick={generate.cancel}
              className="rounded-lg border border-indigo-300 px-3 py-1 text-xs font-medium text-indigo-600 hover:bg-indigo-100"
            >
              Cancel
            </button>
          )}
          <button
            onClick={generate.mutate}
            disabled={generate.isPending}
            className="flex items-center gap-1.5 rounded-lg bg-indigo-600 px-3 py-1 text-xs font-medium text-white hover:bg-indigo-700 disabled:opacity-60"
          >
            {generate.isPending ? (
              <>
                <Spinner className="h-3 w-3" />
                Generating…
              </>
            ) : (
              '✦ Generate'
            )}
          </button>
        </div>
      </div>

      {generate.isError && (
        <p className="mb-2 text-xs text-red-600">Failed to generate plan. Try again.</p>
      )}

      {isLoading && (
        <div className="flex items-center gap-2 text-indigo-600">
          <Spinner className="h-4 w-4" />
          <span className="text-sm">Loading…</span>
        </div>
      )}

      {!isLoading && !plan && !generate.isPending && (
        <p className="text-sm text-indigo-600">
          No plan yet for today. Hit Generate to get AI-powered prioritization.
        </p>
      )}

      {generate.isPending && !plan && (
        <div className="flex items-center gap-2 text-indigo-600">
          <Spinner className="h-4 w-4" />
          <span className="text-sm">Asking Claude to prioritize your tasks…</span>
        </div>
      )}

      {plan && (
        <>
          <div className="mb-1 flex items-center justify-between">
            <p className="text-sm text-indigo-800">{plan.summary}</p>
            <span className="ml-2 shrink-0 text-xs text-indigo-400">
              {new Date(plan.generated_at).toLocaleDateString(undefined, {
                weekday: 'short', month: 'short', day: 'numeric',
              })}
            </span>
          </div>
          <ol className="mt-2 space-y-1">
            {(expanded ? plan.items : plan.items.slice(0, 5)).map((item) => (
              <li key={item.task_id} className="flex items-start gap-2 text-sm">
                <span className="mt-0.5 flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-indigo-600 text-xs font-bold text-white">
                  {item.rank}
                </span>
                <div>
                  <span className="font-medium text-gray-900">{item.task_title}</span>
                  {item.rationale && (
                    <span className="ml-1 text-gray-500">— {item.rationale}</span>
                  )}
                </div>
              </li>
            ))}
          </ol>
          {plan.items.length > 5 && (
            <button
              onClick={() => setExpanded((v) => !v)}
              className="mt-2 pl-7 text-xs font-medium text-indigo-500 hover:text-indigo-700"
            >
              {expanded ? '▲ Show less' : `▼ Show ${plan.items.length - 5} more`}
            </button>
          )}
        </>
      )}
    </div>
  )
}
