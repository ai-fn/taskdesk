import { useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { useForm } from 'react-hook-form'
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
  const { login } = useAuth()
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  const oauthError = oauthErrorMessage(searchParams.get('oauth_error'))
  const [apiError, setApiError] = useState(oauthError ?? '')

  const {
    register,
    handleSubmit,
    formState: { errors, isSubmitting },
  } = useForm<FormValues>()

  async function onSubmit(values: FormValues) {
    setApiError('')
    try {
      await login(values.email, values.password)
      navigate('/')
    } catch (err) {
      if (axios.isAxiosError(err) && err.response?.data?.error) {
        setApiError(err.response.data.error as string)
      } else {
        setApiError('Login failed. Please try again.')
      }
    }
  }

  return (
    <AuthLayout>
      <h1 className="mb-6 text-xl font-bold text-gray-900 dark:text-gray-100">Sign in</h1>
      {apiError && <div className="mb-4"><ErrorMessage message={apiError} /></div>}
      <form onSubmit={handleSubmit(onSubmit)} className="flex flex-col gap-4">
        <Input
          label="Email"
          type="email"
          autoComplete="email"
          error={errors.email?.message}
          {...register('email', { required: 'Email is required' })}
        />
        <Input
          label="Password"
          type="password"
          autoComplete="current-password"
          error={errors.password?.message}
          {...register('password', { required: 'Password is required' })}
        />
        <Button type="submit" loading={isSubmitting} className="mt-2 w-full justify-center">
          Sign in
        </Button>
      </form>
      <OAuthButtons />
      <p className="mt-4 text-center text-sm text-gray-500 dark:text-gray-400">
        No account?{' '}
        <Link to="/signup" className="text-indigo-600 dark:text-indigo-400 hover:underline">
          Sign up
        </Link>
      </p>
    </AuthLayout>
  )
}
