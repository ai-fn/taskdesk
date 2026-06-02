import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { usePlan, useGeneratePlan } from '@/hooks/usePlan'
import { Spinner } from '@/components/ui/Spinner'
import { PaywallModal } from '@/components/ui/PaywallModal'
import { useAuth } from '@/hooks/useAuth'
import { toBCP47 } from '@/i18n/config'

const LS_KEY = 'dailyPlanCollapsed'

function useDots(active: boolean) {
  const [count, setCount] = useState(1)
  useEffect(() => {
    if (!active) { setCount(1); return }
    const id = setInterval(() => setCount((c) => (c >= 3 ? 1 : c + 1)), 350)
    return () => clearInterval(id)
  }, [active])
  return '.'.repeat(count)
}

export function DailyPlanPanel() {
  const { t, i18n } = useTranslation()
  const { plan: userPlan } = useAuth()
  const { data, isLoading } = usePlan()
  const generate = useGeneratePlan()
  const [expanded, setExpanded] = useState(false)
  const [paywallOpen, setPaywallOpen] = useState(false)
  const [collapsed, setCollapsed] = useState(() => {
    try { return localStorage.getItem(LS_KEY) !== 'false' } catch { return true }
  })
  const dots = useDots(generate.isPending)

  // Must be declared before any early return to satisfy Rules of Hooks.
  useEffect(() => {
    if (generate.isPending) setCollapsed(false)
  }, [generate.isPending])

  useEffect(() => {
    try { localStorage.setItem(LS_KEY, String(collapsed)) } catch { /* ignore */ }
  }, [collapsed])

  // Render locked teaser for free-plan users
  if (userPlan !== 'pro') {
    return (
      <>
        <div
          className="group relative mb-6 cursor-pointer select-none overflow-hidden rounded-xl border border-indigo-100 bg-gradient-to-b from-indigo-50/80 to-white/40 transition-all duration-200 hover:border-indigo-200 hover:shadow-md hover:shadow-indigo-100/60 dark:border-indigo-900/40 dark:from-indigo-950/30 dark:to-gray-900/20 dark:hover:border-indigo-800/60 dark:hover:shadow-indigo-950/40"
          onClick={() => setPaywallOpen(true)}
          role="button"
          tabIndex={0}
          aria-label={t('paywall.proFeature')}
          onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') setPaywallOpen(true) }}
        >
          {/* Header row — fully legible, no blur */}
          <div className="flex min-w-0 w-full items-center gap-2 px-4 py-3">
            <span className="shrink-0 text-indigo-400 dark:text-indigo-500 text-sm">✦</span>
            <span className="truncate font-semibold text-indigo-900/80 dark:text-indigo-300/70 text-sm">{t('plan.title')}</span>
            <span className="shrink-0 ml-auto inline-flex items-center gap-1 rounded-full border border-indigo-200 bg-white/80 px-2 py-0.5 text-[10px] font-bold uppercase tracking-widest text-indigo-400 dark:border-indigo-800/60 dark:bg-indigo-950/60 dark:text-indigo-500">
              Pro
            </span>
          </div>

          {/* Skeleton content suggesting plan text */}
          <div className="space-y-2 px-4 pb-2">
            <div className="h-1.5 w-[78%] animate-pulse rounded-full bg-indigo-100 dark:bg-indigo-900/50" />
            <div className="h-1.5 w-[55%] animate-pulse rounded-full bg-indigo-100/70 dark:bg-indigo-900/35" style={{ animationDelay: '150ms' }} />
            <div className="h-1.5 w-[65%] animate-pulse rounded-full bg-indigo-100/50 dark:bg-indigo-900/25" style={{ animationDelay: '300ms' }} />
          </div>

          {/* Bottom gradient mask */}
          <div className="pointer-events-none absolute inset-x-0 bottom-0 h-10 bg-gradient-to-t from-white/60 to-transparent dark:from-gray-900/40" />

          {/* Upgrade CTA strip */}
          <div className="flex items-center justify-center gap-1.5 border-t border-indigo-100/60 px-4 py-2.5 dark:border-indigo-900/30">
            <span className="whitespace-nowrap text-[11px] font-medium text-indigo-400 transition-colors group-hover:text-indigo-600 dark:text-indigo-500 dark:group-hover:text-indigo-300">
              {t('paywall.unlockFeature')}
            </span>
            <svg className="h-3 w-3 translate-x-0 text-indigo-400 transition-transform duration-200 group-hover:translate-x-0.5 group-hover:text-indigo-600 dark:text-indigo-500 dark:group-hover:text-indigo-300" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2.5} strokeLinecap="round" strokeLinejoin="round">
              <path d="M5 12h14M12 5l7 7-7 7" />
            </svg>
          </div>
        </div>
        {paywallOpen && (
          <PaywallModal onClose={() => setPaywallOpen(false)} featureName={t('plan.title')} />
        )}
      </>
    )
  }

  const plan = data?.plan

  // Teaser text shown in collapsed bar
  function teaserText() {
    if (generate.isPending) return t('plan.generating')
    if (plan) return t('plan.tasksRanked', { count: plan.items.length })
    return t('plan.noPlanShort')
  }

  return (
    <div className="mb-6 rounded-xl border border-indigo-200 dark:border-indigo-800 bg-indigo-50 dark:bg-indigo-950/30 overflow-hidden">
      {/* ── Header row (always visible) ──────────────────────────────────── */}
      <button
        type="button"
        onClick={() => setCollapsed((v) => !v)}
        aria-expanded={!collapsed}
        aria-label={t('plan.toggle')}
        className="flex min-w-0 w-full items-center gap-2 px-4 py-3 text-left"
      >
        <span className="shrink-0 text-indigo-500 dark:text-indigo-400 text-sm select-none">✦</span>
        <span className="shrink-0 font-semibold text-indigo-900 dark:text-indigo-300 text-sm">{t('plan.title')}</span>

        {/* Teaser / loading indicator */}
        {isLoading ? (
          <Spinner className="ml-1 h-3.5 w-3.5 text-indigo-400 shrink-0" />
        ) : (
          <span className="ml-1 truncate text-xs text-indigo-500 dark:text-indigo-400 flex-1">
            {generate.isPending ? (
              <>{t('plan.generating')}<span className="inline-block w-4 text-left">{dots}</span></>
            ) : (
              teaserText()
            )}
          </span>
        )}

        {/* Generate button — stopPropagation so it doesn't toggle collapse */}
        <span
          onClick={(e) => {
            e.stopPropagation()
            if (generate.isPending) generate.cancel()
            else generate.mutate()
          }}
          role="button"
          tabIndex={0}
          onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.stopPropagation(); if (generate.isPending) generate.cancel(); else generate.mutate() } }}
          className="shrink-0 flex items-center gap-1 rounded-md bg-indigo-600 dark:bg-indigo-700 px-2.5 py-1 text-xs font-medium text-white hover:bg-indigo-700 dark:hover:bg-indigo-600 transition-colors"
        >
          {generate.isPending ? (
            <>
              <div className="relative inline-flex items-center justify-center">
                <Spinner className="h-3 w-3" />
                <svg className="absolute h-2.5 w-2.5" viewBox="0 0 24 24" fill="white">
                  <rect x="4" y="4" width="16" height="16" rx="1" />
                </svg>
              </div>
              {t('plan.generating')}
            </>
          ) : (
            t('plan.generate')
          )}
        </span>

        {/* Chevron */}
        <svg
          className={`shrink-0 h-4 w-4 text-indigo-400 dark:text-indigo-500 transition-transform duration-200 ${collapsed ? '' : 'rotate-180'}`}
          viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2}
        >
          <path strokeLinecap="round" strokeLinejoin="round" d="M19 9l-7 7-7-7" />
        </svg>
      </button>

      {/* ── Expandable body ───────────────────────────────────────────────── */}
      <div
        style={{ gridTemplateRows: collapsed ? '0fr' : '1fr' }}
        className="grid transition-[grid-template-rows] duration-300 ease-in-out"
      >
        <div className="overflow-hidden">
          <div className="border-t border-indigo-200 dark:border-indigo-800 px-4 pb-4 pt-3">
            {generate.errorMessage && (
              <p className="mb-2 text-xs text-red-600 dark:text-red-400">
                {generate.errorMessage.includes('no tasks') ? t('plan.addTasksFirst') : t('plan.failed')}
              </p>
            )}

            {!plan && !generate.isPending && !isLoading && (
              <p className="text-sm text-indigo-600 dark:text-indigo-400">{t('plan.noplan')}</p>
            )}

            {generate.isPending && (
              <div className="text-indigo-700 dark:text-indigo-300">
                <p className="text-sm italic">
                  {generate.deltas.length === 0 ? (
                    <>{t('plan.askingClaude')}<span className="inline-block w-4 text-left">{dots}</span></>
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
              <div key={plan.plan_id}>
                <div className="mb-1 flex items-center justify-between">
                  <p className="animate-fade-in text-sm text-indigo-800 dark:text-indigo-200">{plan.summary}</p>
                  <span
                    className="ml-2 shrink-0 animate-fade-in text-xs text-indigo-400 dark:text-indigo-500"
                    style={{ animationDelay: '60ms' }}
                  >
                    {new Date(plan.generated_at).toLocaleDateString(toBCP47(i18n.language), {
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
                    <div
                      style={{ gridTemplateRows: expanded ? '1fr' : '0fr' }}
                      className="grid transition-[grid-template-rows] duration-300 ease-in-out"
                    >
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
                      {expanded ? t('plan.showLess') : t('plan.showMore', { count: plan.items.length - 5 })}
                    </button>
                  </>
                )}
              </div>
            )}
          </div>
        </div>
      </div>
    </div>
  )
}
