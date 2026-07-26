import { useCallback, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/Button'
import { Input } from '@/components/ui/Input'
import { OTPInput } from '@/components/ui/OTPInput'
import { PasswordStrengthBar } from '@/components/ui/PasswordStrengthBar'
import { scorePassword, MIN_PASSWORD_SCORE } from '@/lib/passwordStrength'
import { Spinner } from '@/components/ui/Spinner'
import { ErrorMessage } from '@/components/ui/ErrorMessage'
import { ConfirmDialog } from '@/components/ui/ConfirmDialog'
import { ToastContainer, type ToastMessage } from '@/components/ui/Toast'
import { LanguageSwitcher } from '@/components/account/LanguageSwitcher'
import { useSessions, useRevokeSession, useRevokeOtherSessions } from '@/hooks/useSessions'
import { toBCP47 } from '@/i18n/config'
import { useAuth } from '@/hooks/useAuth'
import * as accountApi from '@/api/account'
import * as authApi from '@/api/auth'
import axios from 'axios'

type Confirm =
  | { type: 'revoke'; sessionId: string }
  | { type: 'revoke-others' }
  | null

// ── Shared section card ───────────────────────────────────────────────────────

function SectionCard({
  icon,
  title,
  description,
  badge,
  children,
}: {
  icon: React.ReactNode
  title: string
  description?: string
  badge?: React.ReactNode
  children?: React.ReactNode
}) {
  return (
    <section className="rounded-xl bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-800 shadow-sm overflow-hidden">
      <div className="flex items-start gap-4 p-5">
        <div className="mt-0.5 flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-gray-100 dark:bg-gray-800">
          {icon}
        </div>
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2 flex-wrap">
            <h2 className="text-sm font-semibold text-gray-900 dark:text-gray-100">{title}</h2>
            {badge}
          </div>
          {description && (
            <p className="mt-0.5 text-xs text-gray-500 dark:text-gray-400">{description}</p>
          )}
        </div>
      </div>
      {children && (
        <div className="border-t border-gray-100 dark:border-gray-800 px-5 pb-5 pt-4">
          {children}
        </div>
      )}
    </section>
  )
}

// ── Password section ──────────────────────────────────────────────────────────

type PasswordStep = 'idle' | 'sent'

interface ResetFormValues {
  newPassword: string
}

function PasswordSection({ addToast }: { addToast: (text: string) => void }) {
  const { t } = useTranslation()
  const { email, logout } = useAuth()
  const [step, setStep] = useState<PasswordStep>('idle')
  const [otp, setOtp] = useState('')
  const [requesting, setRequesting] = useState(false)
  const [apiError, setApiError] = useState('')
  const { register, handleSubmit, reset, watch, formState: { errors, isSubmitting } } = useForm<ResetFormValues>()
  const passwordValue = watch('newPassword', '')

  async function handleSendCode() {
    if (!email) return
    setRequesting(true)
    setApiError('')
    try {
      await authApi.requestPasswordReset(email)
      setStep('sent')
    } catch (err) {
      if (axios.isAxiosError(err) && err.response?.data?.error) {
        setApiError(err.response.data.error as string)
      } else {
        setApiError(t('account.password.resetFailed'))
      }
    } finally {
      setRequesting(false)
    }
  }

  async function onSubmit(values: ResetFormValues) {
    if (!email) return
    if (otp.length < 6) { setApiError(t('auth.forgotPassword.codeIncomplete')); return }
    setApiError('')
    try {
      await authApi.resetPassword(email, otp, values.newPassword)
      addToast(t('account.password.resetSuccess'))
      reset()
      setOtp('')
      setStep('idle')
      await logout()
    } catch (err) {
      if (axios.isAxiosError(err) && err.response?.data?.error) {
        setApiError(err.response.data.error as string)
      } else {
        setApiError(t('account.password.resetFailed'))
      }
    }
  }

  const lockIcon = (
    <svg className="h-4 w-4 text-gray-500 dark:text-gray-400" fill="none" viewBox="0 0 24 24" strokeWidth={2} stroke="currentColor">
      <path strokeLinecap="round" strokeLinejoin="round" d="M16.5 10.5V6.75a4.5 4.5 0 10-9 0v3.75m-.75 11.25h10.5a2.25 2.25 0 002.25-2.25v-6.75a2.25 2.25 0 00-2.25-2.25H6.75a2.25 2.25 0 00-2.25 2.25v6.75a2.25 2.25 0 002.25 2.25z" />
    </svg>
  )

  return (
    <SectionCard
      icon={lockIcon}
      title={t('account.password.title')}
      description={t('account.password.description')}
    >
      {apiError && <div className="mb-4"><ErrorMessage message={apiError} /></div>}

      {step === 'idle' ? (
        <div className="flex items-center justify-between gap-4">
          <p className="text-xs text-gray-500 dark:text-gray-400">
            {t('account.password.prefillDescription', { email: email ?? '' })}
          </p>
          <Button variant="secondary" loading={requesting} onClick={handleSendCode} className="shrink-0">
            {t('account.password.reset')}
          </Button>
        </div>
      ) : (
        <form onSubmit={handleSubmit(onSubmit)} className="space-y-4 animate-fade-in">
          <div className="space-y-1.5">
            <p className="text-xs font-medium text-gray-700 dark:text-gray-300">{t('auth.forgotPassword.codeLabel')}</p>
            <OTPInput value={otp} onChange={setOtp} autoFocus error={!!apiError && otp.length < 6} />
          </div>
          <div>
            <Input
              label={t('auth.forgotPassword.newPasswordLabel')}
              type="password"
              autoComplete="new-password"
              error={errors.newPassword?.message}
              {...register('newPassword', {
                required: t('auth.forgotPassword.newPasswordRequired'),
                minLength: { value: 8, message: t('auth.forgotPassword.newPasswordMinLength') },
                validate: (v) => scorePassword(v) >= MIN_PASSWORD_SCORE || t('auth.password.tooWeak'),
              })}
            />
            <PasswordStrengthBar password={passwordValue} />
          </div>
          <div className="flex gap-2">
            <Button type="submit" loading={isSubmitting} disabled={otp.length < 6}>
              {t('auth.forgotPassword.step2Submit')}
            </Button>
            <Button
              variant="ghost"
              type="button"
              onClick={() => { setStep('idle'); setApiError(''); setOtp(''); reset() }}
            >
              {t('common.cancel')}
            </Button>
          </div>
        </form>
      )}
    </SectionCard>
  )
}

// ── Email verification section ────────────────────────────────────────────────

function EmailVerifySection({ addToast }: { addToast: (text: string) => void }) {
  const { t } = useTranslation()
  const { emailVerified, setEmailVerified } = useAuth()
  const [otp, setOtp] = useState('')
  const [codeSent, setCodeSent] = useState(false)
  const [requesting, setRequesting] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [apiError, setApiError] = useState('')

  async function handleSendCode() {
    setRequesting(true)
    setApiError('')
    try {
      await accountApi.requestEmailVerification()
      setCodeSent(true)
      setOtp('')
    } catch (err) {
      if (axios.isAxiosError(err) && err.response?.status === 409) {
        setApiError(t('account.emailVerify.alreadyVerified'))
      } else if (axios.isAxiosError(err) && err.response?.data?.error) {
        setApiError(err.response.data.error as string)
      } else {
        setApiError(t('account.emailVerify.requestFailed'))
      }
    } finally {
      setRequesting(false)
    }
  }

  async function handleConfirm() {
    if (otp.length < 6) { setApiError(t('auth.forgotPassword.codeIncomplete')); return }
    setSubmitting(true)
    setApiError('')
    try {
      await accountApi.confirmEmail(otp)
      setEmailVerified()
      addToast(t('account.emailVerify.verifySuccess'))
      setOtp('')
      setCodeSent(false)
    } catch (err) {
      if (axios.isAxiosError(err) && err.response?.data?.error) {
        setApiError(err.response.data.error as string)
      } else {
        setApiError(t('account.emailVerify.verifyFailed'))
      }
    } finally {
      setSubmitting(false)
    }
  }

  const envelopeIcon = (
    <svg className="h-4 w-4 text-gray-500 dark:text-gray-400" fill="none" viewBox="0 0 24 24" strokeWidth={2} stroke="currentColor">
      <path strokeLinecap="round" strokeLinejoin="round" d="M21.75 6.75v10.5a2.25 2.25 0 01-2.25 2.25h-15a2.25 2.25 0 01-2.25-2.25V6.75m19.5 0A2.25 2.25 0 0019.5 4.5h-15a2.25 2.25 0 00-2.25 2.25m19.5 0v.243a2.25 2.25 0 01-1.07 1.916l-7.5 4.615a2.25 2.25 0 01-2.36 0L3.32 8.91a2.25 2.25 0 01-1.07-1.916V6.75" />
    </svg>
  )

  const verifiedBadge = (
    <span className="inline-flex items-center gap-1 rounded-full bg-green-100 dark:bg-green-950/50 border border-green-200 dark:border-green-800 px-2 py-0.5 text-xs font-medium text-green-700 dark:text-green-400">
      <svg className="h-3 w-3" fill="none" viewBox="0 0 24 24" strokeWidth={2.5} stroke="currentColor">
        <path strokeLinecap="round" strokeLinejoin="round" d="M4.5 12.75l6 6 9-13.5" />
      </svg>
      {t('account.emailVerify.verified')}
    </span>
  )

  const unverifiedBadge = (
    <span className="inline-flex items-center gap-1 rounded-full bg-amber-50 dark:bg-amber-950/40 border border-amber-200 dark:border-amber-800 px-2 py-0.5 text-xs font-medium text-amber-700 dark:text-amber-400">
      <svg className="h-3 w-3" fill="none" viewBox="0 0 24 24" strokeWidth={2.5} stroke="currentColor">
        <path strokeLinecap="round" strokeLinejoin="round" d="M12 9v3.75m-9.303 3.376c-.866 1.5.217 3.374 1.948 3.374h14.71c1.73 0 2.813-1.874 1.948-3.374L13.949 3.378c-.866-1.5-3.032-1.5-3.898 0L2.697 16.126z" />
      </svg>
      {t('account.emailVerify.unverified')}
    </span>
  )

  if (emailVerified) {
    return (
      <SectionCard
        icon={envelopeIcon}
        title={t('account.emailVerify.title')}
        badge={verifiedBadge}
      />
    )
  }

  return (
    <SectionCard
      icon={envelopeIcon}
      title={t('account.emailVerify.title')}
      description={t('account.emailVerify.unverifiedDescription')}
      badge={unverifiedBadge}
    >
      {apiError && <div className="mb-4"><ErrorMessage message={apiError} /></div>}

      {!codeSent ? (
        <Button variant="secondary" loading={requesting} onClick={handleSendCode}>
          {t('account.emailVerify.requestCode')}
        </Button>
      ) : (
        <div className="space-y-4 animate-fade-in">
          <p className="text-xs text-gray-500 dark:text-gray-400">{t('account.emailVerify.codeSent')}</p>
          <div className="space-y-1.5">
            <p className="text-xs font-medium text-gray-700 dark:text-gray-300">{t('account.emailVerify.codeLabel')}</p>
            <OTPInput value={otp} onChange={setOtp} autoFocus error={!!apiError} />
          </div>
          <div className="flex gap-2">
            <Button
              type="button"
              loading={submitting}
              disabled={otp.length < 6}
              onClick={handleConfirm}
            >
              {t('account.emailVerify.confirm')}
            </Button>
            <Button
              variant="ghost"
              type="button"
              onClick={() => { setCodeSent(false); setApiError(''); setOtp('') }}
            >
              {t('common.cancel')}
            </Button>
          </div>
        </div>
      )}
    </SectionCard>
  )
}

// ── AccountPage ───────────────────────────────────────────────────────────────

export function AccountPage() {
  const { t, i18n } = useTranslation()
  const { data: sessions = [], isLoading } = useSessions()
  const revokeSession = useRevokeSession()
  const revokeOthers = useRevokeOtherSessions()
  const [confirm, setConfirm] = useState<Confirm>(null)
  const [toasts, setToasts] = useState<ToastMessage[]>([])

  const addToast = (text: string) =>
    setToasts((prev) => [...prev, { id: Date.now(), text }])

  const dismissToast = useCallback((id: number) => {
    setToasts((prev) => prev.filter((t) => t.id !== id))
  }, [])

  const locale = toBCP47(i18n.language)

  function handleConfirm() {
    if (!confirm) return
    if (confirm.type === 'revoke') {
      revokeSession.mutate(confirm.sessionId, {
        onSuccess: () => addToast(t('account.sessions.revokeSuccess')),
        onError: () => addToast(t('account.sessions.revokeFailed')),
      })
    } else {
      revokeOthers.mutate(undefined, {
        onSuccess: (n) => addToast(t('account.sessions.signedOutOthers', { count: n })),
        onError: () => addToast(t('account.sessions.revokeOthersFailed')),
      })
    }
    setConfirm(null)
  }

  const isPending = revokeSession.isPending || revokeOthers.isPending

  return (
    <div className="space-y-6">
      <h1 className="text-2xl font-bold text-gray-900 dark:text-gray-100">{t('account.title')}</h1>

      {/* Email Verification */}
      <EmailVerifySection addToast={addToast} />

      {/* Password */}
      <PasswordSection addToast={addToast} />

      {/* Language */}
      <SectionCard
        icon={
          <svg className="h-4 w-4 text-gray-500 dark:text-gray-400" fill="none" viewBox="0 0 24 24" strokeWidth={2} stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" d="M10.5 21l5.25-11.25L21 21m-9-3h7.5M3 5.621a48.474 48.474 0 016-.371m0 0c1.12 0 2.233.038 3.334.114M9 5.25V3m3.334 2.364C11.176 10.658 7.69 15.08 3 17.502m9.334-12.138c.896.061 1.785.147 2.666.257m-4.589 8.495a18.023 18.023 0 01-3.827-5.802" />
          </svg>
        }
        title={t('account.language.title')}
      >
        <LanguageSwitcher />
      </SectionCard>

      {/* Sessions */}
      <section className="space-y-3">
        <div className="flex items-center justify-between">
          <div>
            <h2 className="text-sm font-semibold text-gray-900 dark:text-gray-100">{t('account.sessions.title')}</h2>
            <p className="text-xs text-gray-500 dark:text-gray-400">{t('account.sessions.description')}</p>
          </div>
          {sessions.length > 1 && (
            <Button
              variant="danger"
              onClick={() => setConfirm({ type: 'revoke-others' })}
              disabled={isPending}
            >
              {t('account.sessions.revokeOthers')}
            </Button>
          )}
        </div>

        {isLoading ? (
          <div className="flex justify-center py-8">
            <Spinner className="h-6 w-6 text-indigo-500" />
          </div>
        ) : sessions.length === 0 ? (
          <p className="text-sm text-gray-500 dark:text-gray-400">{t('account.sessions.empty')}</p>
        ) : (
          <div className="space-y-2">
            {sessions.map((session) => (
              <div
                key={session.id}
                className="rounded-xl bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-800 p-4 shadow-sm"
              >
                <div className="flex items-start justify-between gap-4">
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-2 flex-wrap">
                      <span className="font-medium text-gray-900 dark:text-gray-100 truncate">
                        {session.browser || t('account.sessions.unknownBrowser')}
                        {session.os ? ` ${session.os}` : ''}
                      </span>
                      <span className="text-xs text-gray-500 dark:text-gray-400">
                        {t(`account.sessions.device.${session.device.toLowerCase()}`, { defaultValue: session.device })}
                      </span>
                      {session.current && (
                        <span className="inline-flex items-center rounded-full bg-indigo-100 dark:bg-indigo-900 px-2 py-0.5 text-xs font-medium text-indigo-700 dark:text-indigo-300">
                          {t('account.sessions.current')}
                        </span>
                      )}
                    </div>
                    <div className="mt-1 flex flex-wrap gap-x-4 gap-y-1 text-xs text-gray-500 dark:text-gray-400">
                      {session.ip_address && (
                        <span>{t('account.sessions.ip')} {session.ip_address}</span>
                      )}
                      <span>
                        {t('account.sessions.lastActive')}{' '}
                        {new Date(session.last_active_at).toLocaleString(locale, {
                          dateStyle: 'medium',
                          timeStyle: 'short',
                        })}
                      </span>
                      <span>
                        {t('account.sessions.signedIn')}{' '}
                        {new Date(session.created_at).toLocaleString(locale, {
                          dateStyle: 'medium',
                          timeStyle: 'short',
                        })}
                      </span>
                    </div>
                  </div>
                  {!session.current && (
                    <Button
                      variant="ghost"
                      className="shrink-0 text-red-600 dark:text-red-400 hover:bg-red-50 dark:hover:bg-red-900/30"
                      onClick={() => setConfirm({ type: 'revoke', sessionId: session.id })}
                      disabled={isPending}
                    >
                      {t('account.sessions.revoke')}
                    </Button>
                  )}
                </div>
              </div>
            ))}
          </div>
        )}
      </section>

      {confirm && (
        <ConfirmDialog
          title={confirm.type === 'revoke'
            ? t('account.sessions.revokeConfirm')
            : t('account.sessions.revokeOthersConfirm')}
          message={confirm.type === 'revoke'
            ? t('account.sessions.revokeBody')
            : t('account.sessions.revokeOthersBody')}
          confirmText={confirm.type === 'revoke'
            ? t('account.sessions.revoke')
            : t('account.sessions.revokeOthers')}
          variant="danger"
          isLoading={isPending}
          onConfirm={handleConfirm}
          onCancel={() => setConfirm(null)}
        />
      )}

      <ToastContainer toasts={toasts} onDismiss={dismissToast} />
    </div>
  )
}
