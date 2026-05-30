import { useState } from 'react'
import { Link } from 'react-router-dom'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { AuthLayout } from '@/components/layout/AuthLayout'
import { Input } from '@/components/ui/Input'
import { Button } from '@/components/ui/Button'
import { ErrorMessage } from '@/components/ui/ErrorMessage'
import { OTPInput } from '@/components/ui/OTPInput'
import * as authApi from '@/api/auth'
import axios from 'axios'

type Step = 'request' | 'reset' | 'done'

interface RequestFormValues { email: string }
interface ResetFormValues { newPassword: string }

// ── Step progress dots ────────────────────────────────────────────────────────

function StepDots({ step }: { step: Step }) {
  const steps: Step[] = ['request', 'reset', 'done']
  const current = steps.indexOf(step)
  return (
    <div className="flex items-center justify-center gap-2 mb-8">
      {steps.map((s, i) => (
        <div key={s} className="flex items-center gap-2">
          <div
            className={`rounded-full transition-all duration-300 ${
              i < current
                ? 'w-2 h-2 bg-indigo-400 dark:bg-indigo-500'
                : i === current
                  ? 'w-3 h-3 bg-indigo-600 dark:bg-indigo-400 ring-4 ring-indigo-100 dark:ring-indigo-900/60'
                  : 'w-2 h-2 bg-gray-200 dark:bg-gray-700'
            }`}
          />
          {i < steps.length - 1 && (
            <div
              className={`h-px w-8 transition-all duration-500 ${
                i < current ? 'bg-indigo-400 dark:bg-indigo-500' : 'bg-gray-200 dark:bg-gray-700'
              }`}
            />
          )}
        </div>
      ))}
    </div>
  )
}

// ── Step 1 — request reset code ───────────────────────────────────────────────

function RequestStep({ onSuccess }: { onSuccess: (email: string) => void }) {
  const { t } = useTranslation()
  const [apiError, setApiError] = useState('')
  const { register, handleSubmit, formState: { errors, isSubmitting } } = useForm<RequestFormValues>()

  async function onSubmit(values: RequestFormValues) {
    setApiError('')
    try {
      await authApi.requestPasswordReset(values.email)
      onSuccess(values.email)
    } catch (err) {
      if (axios.isAxiosError(err) && err.response?.data?.error) {
        setApiError(err.response.data.error as string)
      } else {
        setApiError(t('auth.forgotPassword.failed'))
      }
    }
  }

  return (
    <div>
      {/* Icon */}
      <div className="flex justify-center mb-5">
        <div className="w-14 h-14 rounded-2xl bg-indigo-50 dark:bg-indigo-950/50 flex items-center justify-center">
          <svg className="w-7 h-7 text-indigo-600 dark:text-indigo-400" fill="none" viewBox="0 0 24 24" strokeWidth={1.75} stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" d="M16.5 10.5V6.75a4.5 4.5 0 10-9 0v3.75m-.75 11.25h10.5a2.25 2.25 0 002.25-2.25v-6.75a2.25 2.25 0 00-2.25-2.25H6.75a2.25 2.25 0 00-2.25 2.25v6.75a2.25 2.25 0 002.25 2.25z" />
          </svg>
        </div>
      </div>

      <h1 className="mb-1.5 text-center text-xl font-bold text-gray-900 dark:text-gray-100">
        {t('auth.forgotPassword.step1Title')}
      </h1>
      <p className="mb-6 text-center text-sm text-gray-500 dark:text-gray-400">
        {t('auth.forgotPassword.step1Description')}
      </p>

      {apiError && <div className="mb-4"><ErrorMessage message={apiError} /></div>}

      <form onSubmit={handleSubmit(onSubmit)} className="flex flex-col gap-4">
        <Input
          label={t('auth.fields.email')}
          type="email"
          autoComplete="email"
          autoFocus
          error={errors.email?.message}
          {...register('email', { required: t('auth.login.emailRequired') })}
        />
        <Button type="submit" loading={isSubmitting} className="mt-1 w-full justify-center">
          {t('auth.forgotPassword.step1Submit')}
        </Button>
      </form>

      <p className="mt-5 text-center text-sm text-gray-500 dark:text-gray-400">
        <Link to="/login" className="text-indigo-600 dark:text-indigo-400 hover:underline">
          {t('auth.forgotPassword.backToLogin')}
        </Link>
      </p>
    </div>
  )
}

// ── Step 2 — enter code + new password ────────────────────────────────────────

function ResetStep({ email, onSuccess }: { email: string; onSuccess: () => void }) {
  const { t } = useTranslation()
  const [otp, setOtp] = useState('')
  const [apiError, setApiError] = useState('')
  const [resendState, setResendState] = useState<'idle' | 'sending' | 'sent'>('idle')
  const { register, handleSubmit, formState: { errors, isSubmitting } } = useForm<ResetFormValues>()

  async function handleResend() {
    setResendState('sending')
    setApiError('')
    try {
      await authApi.requestPasswordReset(email)
      setOtp('')
      setResendState('sent')
    } catch (err) {
      setResendState('idle')
      if (axios.isAxiosError(err) && err.response?.data?.error) {
        setApiError(err.response.data.error as string)
      } else {
        setApiError(t('auth.forgotPassword.failed'))
      }
    }
  }

  async function onSubmit(values: ResetFormValues) {
    if (otp.length < 6) {
      setApiError(t('auth.forgotPassword.codeIncomplete'))
      return
    }
    setApiError('')
    try {
      await authApi.resetPassword(email, otp, values.newPassword)
      onSuccess()
    } catch (err) {
      if (axios.isAxiosError(err) && err.response?.data?.error) {
        setApiError(err.response.data.error as string)
      } else {
        setApiError(t('auth.forgotPassword.failed'))
      }
    }
  }

  return (
    <div>
      <h1 className="mb-1.5 text-center text-xl font-bold text-gray-900 dark:text-gray-100">
        {t('auth.forgotPassword.step2Title')}
      </h1>

      {/* Email pill */}
      <div className="flex justify-center mb-5">
        <span className="inline-flex items-center gap-1.5 rounded-full bg-gray-100 dark:bg-gray-800 border border-gray-200 dark:border-gray-700 px-3 py-1 text-xs font-medium text-gray-600 dark:text-gray-300 max-w-full">
          <svg className="w-3 h-3 shrink-0 text-gray-400 dark:text-gray-500" fill="none" viewBox="0 0 24 24" strokeWidth={2} stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" d="M21.75 6.75v10.5a2.25 2.25 0 01-2.25 2.25h-15a2.25 2.25 0 01-2.25-2.25V6.75m19.5 0A2.25 2.25 0 0019.5 4.5h-15a2.25 2.25 0 00-2.25 2.25m19.5 0v.243a2.25 2.25 0 01-1.07 1.916l-7.5 4.615a2.25 2.25 0 01-2.36 0L3.32 8.91a2.25 2.25 0 01-1.07-1.916V6.75" />
          </svg>
          <span className="truncate">{email}</span>
        </span>
      </div>

      {apiError && <div className="mb-4"><ErrorMessage message={apiError} /></div>}
      {resendState === 'sent' && (
        <div className="mb-4 rounded-md bg-green-50 dark:bg-green-950/40 border border-green-200 dark:border-green-800 px-4 py-3 text-sm text-green-700 dark:text-green-400">
          {t('auth.forgotPassword.resendSent')}
        </div>
      )}

      <form onSubmit={handleSubmit(onSubmit)} className="flex flex-col gap-5">
        {/* OTP boxes */}
        <div className="space-y-2">
          <p className="text-center text-sm font-medium text-gray-700 dark:text-gray-300">
            {t('auth.forgotPassword.codeLabel')}
          </p>
          <OTPInput
            value={otp}
            onChange={setOtp}
            autoFocus
            error={apiError.includes('code') || apiError.includes('验证码')}
          />
        </div>

        <Input
          label={t('auth.forgotPassword.newPasswordLabel')}
          type="password"
          autoComplete="new-password"
          error={errors.newPassword?.message}
          {...register('newPassword', {
            required: t('auth.forgotPassword.newPasswordRequired'),
            minLength: { value: 8, message: t('auth.forgotPassword.newPasswordMinLength') },
          })}
        />

        <Button
          type="submit"
          loading={isSubmitting}
          disabled={otp.length < 6}
          className="w-full justify-center"
        >
          {t('auth.forgotPassword.step2Submit')}
        </Button>
      </form>

      {/* Resend + back links */}
      <div className="mt-5 flex flex-col items-center gap-2 text-sm text-gray-500 dark:text-gray-400">
        <button
          type="button"
          onClick={handleResend}
          disabled={resendState === 'sending'}
          className="text-indigo-600 dark:text-indigo-400 hover:underline disabled:opacity-50 disabled:cursor-not-allowed"
        >
          {resendState === 'sending' ? '…' : t('auth.forgotPassword.resendCode')}
        </button>
        <Link to="/login" className="hover:underline">
          {t('auth.forgotPassword.backToLogin')}
        </Link>
      </div>
    </div>
  )
}

// ── Step 3 — success ──────────────────────────────────────────────────────────

function DoneStep() {
  const { t } = useTranslation()
  return (
    <div className="flex flex-col items-center text-center">
      {/* Animated checkmark */}
      <div className="mb-5 animate-scale-in">
        <div className="w-16 h-16 rounded-full bg-green-100 dark:bg-green-950/60 flex items-center justify-center">
          <svg className="w-8 h-8 text-green-600 dark:text-green-400" fill="none" viewBox="0 0 24 24" strokeWidth={2.5} stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" d="M4.5 12.75l6 6 9-13.5" />
          </svg>
        </div>
      </div>

      <h1 className="mb-2 text-xl font-bold text-gray-900 dark:text-gray-100">
        {t('auth.forgotPassword.successTitle')}
      </h1>
      <p className="mb-7 text-sm text-gray-500 dark:text-gray-400 leading-relaxed">
        {t('auth.forgotPassword.successMessage')}
      </p>
      <Link
        to="/login"
        className="block w-full rounded-lg bg-indigo-600 py-2.5 text-center text-sm font-medium text-white hover:bg-indigo-700 dark:bg-indigo-500 dark:hover:bg-indigo-600 transition-colors"
      >
        {t('auth.forgotPassword.signIn')}
      </Link>
    </div>
  )
}

// ── Page root ─────────────────────────────────────────────────────────────────

export function ForgotPasswordPage() {
  const [step, setStep] = useState<Step>('request')
  const [email, setEmail] = useState('')
  const [contentKey, setContentKey] = useState(0)

  function advance(nextStep: Step, nextEmail?: string) {
    if (nextEmail) setEmail(nextEmail)
    setContentKey(k => k + 1)
    setStep(nextStep)
  }

  return (
    <AuthLayout>
      <StepDots step={step} />
      <div key={contentKey} className="animate-fade-in">
        {step === 'request' && (
          <RequestStep onSuccess={e => advance('reset', e)} />
        )}
        {step === 'reset' && (
          <ResetStep email={email} onSuccess={() => advance('done')} />
        )}
        {step === 'done' && <DoneStep />}
      </div>
    </AuthLayout>
  )
}
