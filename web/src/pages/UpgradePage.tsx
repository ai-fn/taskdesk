import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link, useSearchParams } from 'react-router-dom'
import { useAuth } from '@/hooks/useAuth'
import { createCheckout, redeemPromoCode } from '@/api/subscription'

const FEATURES = [
  { key: 'ai', icon: '✦' },
  { key: 'parse', icon: '✎' },
  { key: 'priority', icon: '↑' },
  { key: 'scheduling', icon: '◷' },
] as const

type PaymentStatus = 'idle' | 'success' | 'canceled'

export function UpgradePage() {
  const { t } = useTranslation()
  const { plan, refreshPlan } = useAuth()
  const isPro = plan === 'pro'
  const [searchParams, setSearchParams] = useSearchParams()
  const [paymentStatus, setPaymentStatus] = useState<PaymentStatus>('idle')

  // Stripe redirects back here with ?success=1 or ?canceled=1 after checkout.
  useEffect(() => {
    if (searchParams.get('success') === '1') {
      setPaymentStatus('success')
      refreshPlan()
      setSearchParams({}, { replace: true })
    } else if (searchParams.get('canceled') === '1') {
      setPaymentStatus('canceled')
      setSearchParams({}, { replace: true })
    }
  }, []) // eslint-disable-line react-hooks/exhaustive-deps

  const [promoVisible, setPromoVisible] = useState(false)
  const [promoCode, setPromoCode] = useState('')
  const [promoError, setPromoError] = useState('')
  const [promoSuccess, setPromoSuccess] = useState(false)
  const [isLoadingCheckout, setIsLoadingCheckout] = useState(false)
  const [isLoadingPromo, setIsLoadingPromo] = useState(false)

  async function handleCheckout() {
    setIsLoadingCheckout(true)
    try {
      const { checkout_url } = await createCheckout()
      window.location.href = checkout_url
    } catch {
      setIsLoadingCheckout(false)
    }
  }

  async function handleRedeem() {
    const code = promoCode.trim()
    if (!code) return
    setIsLoadingPromo(true)
    setPromoError('')
    try {
      await redeemPromoCode(code)
      setPromoSuccess(true)
      await refreshPlan()
    } catch {
      setPromoError(t('upgrade.promoError'))
    } finally {
      setIsLoadingPromo(false)
    }
  }

  return (
    <div className="relative min-h-screen overflow-hidden bg-gray-950">
      {/* Gradient mesh background */}
      <div aria-hidden className="pointer-events-none absolute inset-0">
        <div className="absolute -top-32 left-1/4 h-96 w-96 rounded-full bg-indigo-600/25 blur-3xl" />
        <div className="absolute top-1/3 -right-20 h-80 w-80 rounded-full bg-purple-600/20 blur-3xl" />
        <div className="absolute bottom-0 left-0 h-72 w-72 rounded-full bg-violet-700/15 blur-3xl" />
        {/* Grain overlay */}
        <div
          className="absolute inset-0 opacity-[0.04]"
          style={{
            backgroundImage: `url("data:image/svg+xml,%3Csvg viewBox='0 0 256 256' xmlns='http://www.w3.org/2000/svg'%3E%3Cfilter id='noise'%3E%3CfeTurbulence type='fractalNoise' baseFrequency='0.9' numOctaves='4' stitchTiles='stitch'/%3E%3C/filter%3E%3Crect width='100%25' height='100%25' filter='url(%23noise)'/%3E%3C/svg%3E")`,
            backgroundRepeat: 'repeat',
            backgroundSize: '128px',
          }}
        />
      </div>

      {/* Back link */}
      <div className="relative z-10 px-6 pt-6">
        <Link
          to="/"
          className="inline-flex items-center gap-1.5 text-sm text-gray-400 transition-colors hover:text-gray-200"
        >
          <svg className="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round">
            <path d="M19 12H5M12 19l-7-7 7-7" />
          </svg>
          {t('upgrade.back')}
        </Link>
      </div>

      {/* Main content */}
      <div className="relative z-10 mx-auto max-w-5xl px-6 pb-20 pt-12">

        {/* Payment status banners */}
        {paymentStatus === 'success' && (
          <div className="mb-10 animate-float-up overflow-hidden rounded-2xl border border-emerald-400/20 bg-emerald-500/10 ring-1 ring-emerald-400/10">
            <div className="flex items-center gap-5 px-7 py-6">
              <div className="flex h-12 w-12 shrink-0 items-center justify-center rounded-xl bg-emerald-500/20 ring-1 ring-emerald-400/30">
                <svg className="h-6 w-6 text-emerald-400" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round">
                  <polyline points="20 6 9 17 4 12" />
                </svg>
              </div>
              <div>
                <p className="font-serif-display text-xl italic text-emerald-300">{t('upgrade.successTitle')}</p>
                <p className="mt-0.5 text-sm text-emerald-400/70">{t('upgrade.successMessage')}</p>
              </div>
            </div>
          </div>
        )}

        {paymentStatus === 'canceled' && (
          <div className="mb-10 animate-float-up overflow-hidden rounded-2xl border border-amber-400/20 bg-amber-500/10 ring-1 ring-amber-400/10">
            <div className="flex items-center gap-5 px-7 py-6">
              <div className="flex h-12 w-12 shrink-0 items-center justify-center rounded-xl bg-amber-500/20 ring-1 ring-amber-400/30">
                <svg className="h-6 w-6 text-amber-400" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round">
                  <circle cx="12" cy="12" r="10" />
                  <line x1="12" y1="8" x2="12" y2="12" />
                  <line x1="12" y1="16" x2="12.01" y2="16" />
                </svg>
              </div>
              <div>
                <p className="font-serif-display text-xl italic text-amber-300">{t('upgrade.canceledTitle')}</p>
                <p className="mt-0.5 text-sm text-amber-400/70">{t('upgrade.canceledMessage')}</p>
              </div>
            </div>
          </div>
        )}

        {/* Page headline */}
        <div className="mb-12 text-center animate-fade-in">
          <h1 className="font-serif-display text-4xl italic text-white sm:text-5xl">
            {t('upgrade.title')}
          </h1>
          <p className="mt-3 text-base text-gray-400">{t('upgrade.subtitle')}</p>
        </div>

        <div className="grid gap-8 lg:grid-cols-[1fr_360px] lg:items-start">
          {/* ── Left: Feature cards ───────────────────────────────────────── */}
          <div className="space-y-4">
            <p className="text-xs font-semibold uppercase tracking-wider text-gray-500">
              {t('upgrade.features.title')}
            </p>
            {FEATURES.map(({ key, icon }, i) => (
              <div
                key={key}
                className="animate-float-up flex items-start gap-4 rounded-xl border border-white/[0.07] bg-white/[0.04] p-5 backdrop-blur-sm transition-colors hover:bg-white/[0.07]"
                style={{ animationDelay: `${i * 80}ms` }}
              >
                <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg bg-indigo-500/20 text-lg text-indigo-300 ring-1 ring-indigo-400/30">
                  {icon}
                </div>
                <div>
                  <p className="font-medium text-white">{t(`upgrade.features.${key}`)}</p>
                  <p className="mt-0.5 text-sm text-gray-500">{t(`upgrade.features.${key}Desc`)}</p>
                </div>
              </div>
            ))}
          </div>

          {/* ── Right: Pricing card ───────────────────────────────────────── */}
          <div className="animate-float-up lg:sticky lg:top-8" style={{ animationDelay: '120ms' }}>
            <div className="rounded-2xl border border-white/10 bg-white/[0.06] p-7 shadow-2xl backdrop-blur-xl ring-1 ring-white/5">

              {/* Current plan badge */}
              <div className="mb-5 flex items-center justify-between">
                <span className="text-xs font-medium uppercase tracking-wider text-gray-500">
                  {t('upgrade.currentPlan')}
                </span>
                <span className={`rounded-full px-2.5 py-0.5 text-xs font-semibold ${isPro ? 'bg-indigo-500/20 text-indigo-300 ring-1 ring-indigo-400/30' : 'bg-gray-700 text-gray-300'}`}>
                  {isPro ? t('upgrade.plan.pro') : t('upgrade.plan.free')}
                </span>
              </div>

              {/* Price */}
              <div className="mb-6 flex items-end gap-1">
                <span className="font-serif-display text-5xl italic text-white">{t('upgrade.price')}</span>
                <span className="mb-1.5 text-sm text-gray-400">{t('upgrade.perMonth')}</span>
              </div>

              {isPro ? (
                /* Already pro state */
                <div className="space-y-3">
                  <div className="flex items-center gap-2 rounded-xl bg-indigo-500/10 p-4 ring-1 ring-indigo-400/20">
                    <svg className="h-5 w-5 shrink-0 text-indigo-400" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round">
                      <polyline points="20 6 9 17 4 12" />
                    </svg>
                    <div>
                      <p className="text-sm font-medium text-indigo-200">{t('upgrade.alreadyPro')}</p>
                      <p className="mt-0.5 text-xs text-gray-500">{t('upgrade.alreadyProNote')}</p>
                    </div>
                  </div>
                </div>
              ) : (
                /* Upgrade CTAs */
                <div className="space-y-3">
                  {promoSuccess ? (
                    <div className="flex items-center justify-center gap-2 rounded-xl bg-emerald-500/10 p-4 ring-1 ring-emerald-400/20">
                      <svg className="h-5 w-5 shrink-0 text-emerald-400" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round">
                        <polyline points="20 6 9 17 4 12" />
                      </svg>
                      <p className="text-sm font-medium text-emerald-300">{t('upgrade.promoSuccess')}</p>
                    </div>
                  ) : (
                    <>
                      <button
                        onClick={handleCheckout}
                        disabled={isLoadingCheckout}
                        className="w-full rounded-xl bg-white py-3 text-sm font-semibold text-gray-900 shadow-lg transition-all duration-200 hover:bg-indigo-50 hover:shadow-xl active:scale-[0.98] disabled:opacity-60"
                      >
                        {isLoadingCheckout ? t('upgrade.loading') : t('upgrade.upgradeButton')}
                      </button>

                      <div className="relative flex items-center">
                        <div className="flex-1 border-t border-white/10" />
                        <span className="mx-3 text-xs text-gray-600">{t('upgrade.promoToggle')}</span>
                        <div className="flex-1 border-t border-white/10" />
                      </div>

                      <div>
                        {!promoVisible ? (
                          <button
                            onClick={() => setPromoVisible(true)}
                            className="w-full rounded-lg border border-white/10 py-2.5 text-sm text-gray-400 transition-colors hover:border-white/20 hover:text-gray-300"
                          >
                            {t('upgrade.promoToggle')}
                          </button>
                        ) : (
                          <div className="animate-float-up space-y-2">
                            <div className="flex gap-2">
                              <input
                                type="text"
                                value={promoCode}
                                onChange={(e) => { setPromoCode(e.target.value); setPromoError('') }}
                                onKeyDown={(e) => e.key === 'Enter' && handleRedeem()}
                                placeholder={t('upgrade.promoPlaceholder')}
                                autoFocus
                                className="flex-1 rounded-lg border border-white/10 bg-white/5 px-3 py-2 text-sm text-white placeholder:text-gray-600 focus:border-indigo-500/50 focus:outline-none focus:ring-1 focus:ring-indigo-500/30"
                              />
                              <button
                                onClick={handleRedeem}
                                disabled={isLoadingPromo || !promoCode.trim()}
                                className="rounded-lg bg-indigo-600/80 px-3 py-2 text-sm font-medium text-white transition-colors hover:bg-indigo-500 disabled:opacity-50"
                              >
                                {isLoadingPromo ? '…' : t('upgrade.promoRedeem')}
                              </button>
                            </div>
                            {promoError && (
                              <p className="text-xs text-red-400">{promoError}</p>
                            )}
                          </div>
                        )}
                      </div>
                    </>
                  )}
                </div>
              )}

              {/* Powered by Stripe */}
              <p className="mt-5 flex items-center justify-center gap-1.5 text-xs text-gray-600">
                <svg className="h-3.5 w-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round">
                  <rect x="1" y="4" width="22" height="16" rx="2" ry="2" />
                  <line x1="1" y1="10" x2="23" y2="10" />
                </svg>
                {t('upgrade.poweredBy')}
              </p>
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}
