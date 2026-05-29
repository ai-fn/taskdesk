import { useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { useAuth } from '@/hooks/useAuth'
import { AuthLayout } from '@/components/layout/AuthLayout'
import { Input } from '@/components/ui/Input'
import { Button } from '@/components/ui/Button'
import { ErrorMessage } from '@/components/ui/ErrorMessage'
import { OAuthButtons } from '@/components/auth/OAuthButtons'
import axios from 'axios'

interface FormValues {
  name: string
  email: string
  password: string
}

export function SignupPage() {
  const { t } = useTranslation()
  const { signup } = useAuth()
  const navigate = useNavigate()
  const [apiError, setApiError] = useState('')

  const {
    register,
    handleSubmit,
    formState: { errors, isSubmitting },
  } = useForm<FormValues>()

  async function onSubmit(values: FormValues) {
    setApiError('')
    try {
      await signup(values.email, values.password, values.name)
      navigate('/')
    } catch (err) {
      if (axios.isAxiosError(err) && err.response?.data?.error) {
        setApiError(err.response.data.error as string)
      } else {
        setApiError(t('auth.signup.failed'))
      }
    }
  }

  return (
    <AuthLayout>
      <h1 className="mb-6 text-xl font-bold text-gray-900 dark:text-gray-100">{t('auth.signup.title')}</h1>
      {apiError && <div className="mb-4"><ErrorMessage message={apiError} /></div>}
      <form onSubmit={handleSubmit(onSubmit)} className="flex flex-col gap-4">
        <Input
          label={t('auth.fields.name')}
          autoComplete="name"
          error={errors.name?.message}
          {...register('name', { required: t('auth.signup.nameRequired') })}
        />
        <Input
          label={t('auth.fields.email')}
          type="email"
          autoComplete="email"
          error={errors.email?.message}
          {...register('email', { required: t('auth.signup.emailRequired') })}
        />
        <Input
          label={t('auth.fields.password')}
          type="password"
          autoComplete="new-password"
          error={errors.password?.message}
          {...register('password', {
            required: t('auth.signup.passwordRequired'),
            minLength: { value: 8, message: t('auth.signup.passwordMinLength') },
          })}
        />
        <Button type="submit" loading={isSubmitting} className="mt-2 w-full justify-center">
          {t('auth.signup.submit')}
        </Button>
      </form>
      <OAuthButtons />
      <p className="mt-4 text-center text-sm text-gray-500 dark:text-gray-400">
        {t('auth.signup.haveAccount')}{' '}
        <Link to="/login" className="text-indigo-600 dark:text-indigo-400 hover:underline">
          {t('auth.signup.signIn')}
        </Link>
      </p>
    </AuthLayout>
  )
}
