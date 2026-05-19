import { useEffect, useState } from 'react'
import { usePlan, useGeneratePlan } from '@/hooks/usePlan'
import { Spinner } from '@/components/ui/Spinner'

// useDots returns a string that cycles "." → ".." → "..." every 350ms
// while `active` is true. Stops and resets when `active` goes false.
function useDots(active: boolean) {
  const [count, setCount] = useState(1)
  useEffect(() => {
    if (!active) {
      setCount(1)
      return
    }
    const id = setInterval(() => setCount((c) => (c >= 3 ? 1 : c + 1)), 350)
    return () => clearInterval(id)
  }, [active])
  return '.'.repeat(count)
}

export function DailyPlanPanel() {
  const { data, isLoading } = usePlan()
  const generate = useGeneratePlan()
  const [expanded, setExpanded] = useState(false)
  const dots = useDots(generate.isPending)

  const plan = data?.plan

  return (
    <div className="mb-6 rounded-xl border border-indigo-200 dark:border-indigo-800 bg-indigo-50 dark:bg-indigo-950/30 p-4">
      <div className="mb-2 flex items-center justify-between gap-2">
        <h2 className="font-semibold text-indigo-900 dark:text-indigo-300">AI Daily Plan</h2>
        <div className="flex items-center gap-2">
          <button
            onClick={generate.isPending ? generate.cancel : generate.mutate}
            className="flex items-center gap-1.5 rounded-lg bg-indigo-600 dark:bg-indigo-700 px-3 py-1 text-xs font-medium text-white hover:bg-indigo-700 dark:hover:bg-indigo-600"
          >
            {generate.isPending ? (
              <>
                <div className="relative inline-flex items-center justify-center">
                  <Spinner className="h-4 w-4" />
                  <svg className="absolute h-3.5 w-3.5" viewBox="0 0 24 24" fill="white">
                    <rect x="4" y="4" width="16" height="16" rx="1" />
                  </svg>
                </div>
                Generating…
              </>
            ) : (
              '✦ Generate'
            )}
          </button>
        </div>
      </div>

      {generate.errorMessage && (
        <p className="mb-2 text-xs text-red-600 dark:text-red-400">
          {generate.errorMessage.includes('no tasks')
            ? 'Add some tasks first, then generate a plan.'
            : 'Failed to generate plan. Try again.'}
        </p>
      )}

      {isLoading && (
        <div className="flex items-center gap-2 text-indigo-600 dark:text-indigo-400">
          <Spinner className="h-4 w-4" />
          <span className="text-sm">Loading…</span>
        </div>
      )}

      {!isLoading && !plan && !generate.isPending && (
        <p className="text-sm text-indigo-600 dark:text-indigo-400">
          No plan yet for today. Hit Generate to get AI-powered prioritization.
        </p>
      )}

      {generate.isPending && (
        <div className="text-indigo-700 dark:text-indigo-300">
          <p className="text-sm italic">
            {generate.deltas.length === 0 ? (
              <>Asking Claude<span className="inline-block w-4 text-left">{dots}</span></>
            ) : (
              <>
                {generate.deltas.map((d, i) => (
                  <span key={i} className="inline animate-fade-in">{d}</span>
                ))}
                <span className="ml-0.5 inline-block w-4 text-left not-italic">{dots}</span>
              </>
            )}
          </p>
        </div>
      )}

      {plan && !generate.isPending && (
        // Key by plan_id so a re-generation remounts everything and re-fires
        // the staggered fade-in animations — same task IDs across runs would
        // otherwise reuse the DOM and the items would just pop in.
        <div key={plan.plan_id}>
          <div className="mb-1 flex items-center justify-between">
            <p className="animate-fade-in text-sm text-indigo-800 dark:text-indigo-200">{plan.summary}</p>
            <span
              className="ml-2 shrink-0 animate-fade-in text-xs text-indigo-400 dark:text-indigo-500"
              style={{ animationDelay: '60ms' }}
            >
              {new Date(plan.generated_at).toLocaleDateString(undefined, {
                weekday: 'short', month: 'short', day: 'numeric',
              })}
            </span>
          </div>
          <ol className="mt-2 space-y-1">
            {plan.items.slice(0, 5).map((item, i) => (
              <li
                key={item.task_id}
                className="flex animate-fade-in items-start gap-2 text-sm"
                style={{ animationDelay: `${120 + i * 90}ms` }}
              >
                <span className="mt-0.5 flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-indigo-600 dark:bg-indigo-700 text-xs font-bold text-white">
                  {item.rank}
                </span>
                <div>
                  <span className="font-medium text-gray-900 dark:text-gray-100">{item.task_title}</span>
                  {item.rationale && (
                    <span className="ml-1 text-gray-500 dark:text-gray-400">— {item.rationale}</span>
                  )}
                </div>
              </li>
            ))}
          </ol>
          {plan.items.length > 5 && (
            <>
              {/* grid-template-rows trick: 0fr→1fr gives smooth height expand/collapse */}
              <div
                style={{ gridTemplateRows: expanded ? '1fr' : '0fr' }}
                className="grid transition-[grid-template-rows] duration-300 ease-in-out"
              >
                {/* key remounts on toggle so animate-fade-in replays on each expand */}
                <ol key={expanded ? 'open' : 'closed'} className="mt-1 space-y-1 overflow-hidden">
                  {plan.items.slice(5).map((item, i) => (
                    <li
                      key={item.task_id}
                      className={`flex items-start gap-2 text-sm${expanded ? ' animate-fade-in' : ''}`}
                      style={{ animationDelay: expanded ? `${i * 60}ms` : '0ms' }}
                    >
                      <span className="mt-0.5 flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-indigo-600 dark:bg-indigo-700 text-xs font-bold text-white">
                        {item.rank}
                      </span>
                      <div>
                        <span className="font-medium text-gray-900 dark:text-gray-100">{item.task_title}</span>
                        {item.rationale && (
                          <span className="ml-1 text-gray-500 dark:text-gray-400">— {item.rationale}</span>
                        )}
                      </div>
                    </li>
                  ))}
                </ol>
              </div>
              <button
                onClick={() => setExpanded((v) => !v)}
                className="mt-2 pl-7 text-xs font-medium text-indigo-500 dark:text-indigo-400 hover:text-indigo-700 dark:hover:text-indigo-300"
              >
                {expanded ? '▲ Show less' : `▼ Show ${plan.items.length - 5} more`}
              </button>
            </>
          )}
        </div>
      )}
    </div>
  )
}
