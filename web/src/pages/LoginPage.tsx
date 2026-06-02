import { useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { useAuth } from '@/hooks/useAuth'
import { AuthLayout } from '@/components/layout/AuthLayout'
import { Input } from '@/components/ui/Input'
import { Button } from '@/components/ui/Button'
import { ErrorMessage } from '@/components/ui/ErrorMessage'
import { OAuthButtons, oauthErrorMessage } from '@/components/auth/OAuthButtons'
import axios from 'axios'

interface FormValues {
  email: string
  password: string
}

export function LoginPage() {
  const { t } = useTranslation()
  const { login } = useAuth()
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  const oauthErrorKey = oauthErrorMessage(searchParams.get('oauth_error'))
  const [apiError, setApiError] = useState(oauthErrorKey ? t(oauthErrorKey) : '')

  const {
    register,
    handleSubmit,
    setError,
    formState: { errors, isSubmitting },
  } = useForm<FormValues>()

  async function onSubmit(values: FormValues) {
    setApiError('')
    try {
      await login(values.email, values.password)
      navigate('/')
    } catch (err) {
      if (axios.isAxiosError(err)) {
        const status = err.response?.status
        const msg = err.response?.data?.error as string | undefined
        if (status === 401) {
          // "no password set…" means the account was created via OAuth
          const key = msg?.includes('password set')
            ? 'auth.login.oauthAccount'
            : 'auth.login.invalidCredentials'
          setError('password', { message: t(key) })
        } else if (status === 429) {
          setApiError(t('auth.login.tooManyRequests'))
        } else {
          setApiError(t('auth.login.failed'))
        }
      } else {
        setApiError(t('auth.login.failed'))
      }
    }
  }

  return (
    <AuthLayout>
      <h1 className="mb-6 text-xl font-bold text-gray-900 dark:text-gray-100">{t('auth.login.title')}</h1>
      {apiError && <div className="mb-4"><ErrorMessage message={apiError} /></div>}
      <form onSubmit={handleSubmit(onSubmit)} className="flex flex-col gap-4">
        <Input
          label={t('auth.fields.email')}
          type="email"
          autoComplete="email"
          error={errors.email?.message}
          {...register('email', { required: t('auth.login.emailRequired') })}
        />
        <Input
          label={t('auth.fields.password')}
          type="password"
          autoComplete="current-password"
          error={errors.password?.message}
          {...register('password', { required: t('auth.login.passwordRequired') })}
        />
        <Button type="submit" loading={isSubmitting} className="mt-2 w-full justify-center">
          {t('auth.login.submit')}
        </Button>
      </form>
      <p className="mt-3 text-center text-sm">
        <Link to="/forgot-password" className="text-indigo-600 dark:text-indigo-400 hover:underline">
          {t('auth.login.forgotPassword')}
        </Link>
      </p>
      <OAuthButtons />
      <p className="mt-4 text-center text-sm text-gray-500 dark:text-gray-400">
        {t('auth.login.noAccount')}{' '}
        <Link to="/signup" className="text-indigo-600 dark:text-indigo-400 hover:underline">
          {t('auth.login.signUp')}
        </Link>
      </p>
    </AuthLayout>
  )
}
