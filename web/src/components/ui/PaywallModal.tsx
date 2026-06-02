import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router-dom'
import { Modal } from './Modal'
import { useAuth } from '@/hooks/useAuth'
import { createCheckout, redeemPromoCode } from '@/api/subscription'

interface Props {
  onClose: () => void
  featureName?: string
}

const FEATURES = ['feature1', 'feature2', 'feature3'] as const

export function PaywallModal({ onClose, featureName }: Props) {
  const { t } = useTranslation()
  const { refreshPlan } = useAuth()
  const navigate = useNavigate()
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
      setTimeout(onClose, 1800)
    } catch {
      setPromoError(t('paywall.promoError'))
    } finally {
      setIsLoadingPromo(false)
    }
  }

  function handleViewUpgradePage() {
    onClose()
    navigate('/upgrade')
  }

  return (
    <Modal onClose={onClose} className="max-w-md">
      {/* Card: dark indigo gradient */}
      <div className="relative overflow-hidden rounded-2xl bg-gradient-to-br from-indigo-950 via-indigo-900 to-purple-950 shadow-2xl ring-1 ring-white/10">

        {/* Decorative sparkles */}
        <span aria-hidden className="pointer-events-none absolute left-6 top-7 text-indigo-300/60 animate-twinkle text-lg select-none">✦</span>
        <span aria-hidden className="pointer-events-none absolute right-10 top-12 text-purple-300/50 animate-twinkle-delay text-sm select-none">✦</span>
        <span aria-hidden className="pointer-events-none absolute right-6 bottom-24 text-indigo-400/40 animate-twinkle text-xs select-none">✦</span>

        {/* Radial glow behind lock icon */}
        <div aria-hidden className="pointer-events-none absolute inset-0 flex items-start justify-center">
          <div className="mt-6 h-40 w-40 rounded-full bg-indigo-500/20 blur-3xl" />
        </div>

        <div className="relative px-7 pb-7 pt-8">
          {/* Lock icon */}
          <div className="group mx-auto mb-5 flex h-16 w-16 items-center justify-center rounded-2xl bg-white/10 ring-1 ring-white/20 backdrop-blur-sm transition-all duration-300 hover:bg-white/15 hover:ring-white/30">
            <svg
              className="h-8 w-8 text-indigo-200 transition-transform duration-300 group-hover:scale-110"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth={1.75}
              strokeLinecap="round"
              strokeLinejoin="round"
            >
              <rect x="3" y="11" width="18" height="11" rx="2" />
              <path d="M7 11V7a5 5 0 0 1 10 0v4" />
            </svg>
          </div>

          {/* Headline — DM Serif Display */}
          <h2 className="font-serif-display text-center text-2xl italic text-white">
            {featureName
              ? t('paywall.title')
              : t('paywall.title')}
          </h2>
          <p className="mt-1.5 text-center text-sm text-indigo-300/80">
            {t('paywall.subtitle')}
          </p>

          {/* Feature list */}
          <ul className="mt-6 space-y-2.5">
            {FEATURES.map((key, i) => (
              <li
                key={key}
                className="flex items-center gap-3 animate-float-up"
                style={{ animationDelay: `${i * 60}ms` }}
              >
                <span className="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-indigo-500/30 ring-1 ring-indigo-400/40">
                  <svg className="h-3 w-3 text-indigo-300" viewBox="0 0 12 12" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round">
                    <polyline points="2,6 5,9 10,3" />
                  </svg>
                </span>
                <span className="text-sm text-indigo-100">{t(`paywall.${key}`)}</span>
              </li>
            ))}
          </ul>

          {/* Primary CTA */}
          <button
            onClick={handleCheckout}
            disabled={isLoadingCheckout || promoSuccess}
            className="mt-6 w-full rounded-xl bg-white px-4 py-3 text-sm font-semibold text-indigo-900 shadow-lg transition-all duration-200 hover:bg-indigo-50 hover:shadow-xl active:scale-[0.98] disabled:opacity-60 disabled:cursor-not-allowed"
          >
            {isLoadingCheckout ? t('paywall.loading') : t('paywall.upgradeButton')}
          </button>

          {/* Promo code section */}
          {!promoSuccess ? (
            <div className="mt-3">
              {!promoVisible ? (
                <button
                  onClick={() => setPromoVisible(true)}
                  className="w-full text-center text-xs text-indigo-400 hover:text-indigo-300 transition-colors"
                >
                  {t('paywall.promoToggle')}
                </button>
              ) : (
                <div className="animate-float-up">
                  <div className="flex gap-2">
                    <input
                      type="text"
                      value={promoCode}
                      onChange={(e) => { setPromoCode(e.target.value); setPromoError('') }}
                      onKeyDown={(e) => e.key === 'Enter' && handleRedeem()}
                      placeholder={t('paywall.promoPlaceholder')}
                      autoFocus
                      className="flex-1 rounded-lg border border-white/15 bg-white/10 px-3 py-2 text-sm text-white placeholder:text-indigo-400/60 focus:border-indigo-400/60 focus:outline-none focus:ring-1 focus:ring-indigo-400/40"
                    />
                    <button
                      onClick={handleRedeem}
                      disabled={isLoadingPromo || !promoCode.trim()}
                      className="rounded-lg bg-indigo-600 px-3 py-2 text-sm font-medium text-white transition-colors hover:bg-indigo-500 disabled:opacity-50 disabled:cursor-not-allowed"
                    >
                      {isLoadingPromo ? '…' : t('paywall.promoRedeem')}
                    </button>
                  </div>
                  {promoError && (
                    <p className="mt-1.5 text-xs text-red-400">{promoError}</p>
                  )}
                </div>
              )}
            </div>
          ) : (
            <div className="mt-3 flex items-center justify-center gap-2 animate-float-up">
              <span className="flex h-5 w-5 items-center justify-center rounded-full bg-emerald-500/30 ring-1 ring-emerald-400/40">
                <svg className="h-3 w-3 text-emerald-300" viewBox="0 0 12 12" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round">
                  <polyline points="2,6 5,9 10,3" />
                </svg>
              </span>
              <p className="text-xs font-medium text-emerald-300">{t('paywall.promoSuccess')}</p>
            </div>
          )}

          {/* See full page link */}
          <button
            onClick={handleViewUpgradePage}
            className="mt-4 w-full text-center text-xs text-indigo-500 hover:text-indigo-400 transition-colors"
          >
            {t('paywall.seeAllFeatures')}
          </button>
        </div>
      </div>
    </Modal>
  )
}
