import type { ButtonHTMLAttributes } from 'react'
import { Spinner } from './Spinner'

interface Props extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: 'primary' | 'secondary' | 'danger' | 'ghost'
  loading?: boolean
}

const variants = {
  primary: 'bg-indigo-600 text-white hover:bg-indigo-700 disabled:bg-indigo-300',
  secondary: 'bg-white text-gray-700 border border-gray-300 hover:bg-gray-50 disabled:opacity-50',
  danger: 'bg-red-600 text-white hover:bg-red-700 disabled:bg-red-300',
  ghost: 'text-gray-600 hover:text-gray-900 hover:bg-gray-100 disabled:opacity-50',
}

export function Button({ variant = 'primary', loading, children, className = '', disabled, ...props }: Props) {
  return (
    <button
      className={`inline-flex items-center gap-2 rounded-md px-3 py-2 text-sm font-medium transition-colors focus:outline-none focus:ring-2 focus:ring-indigo-500 focus:ring-offset-1 ${variants[variant]} ${className}`}
      disabled={disabled || loading}
      {...props}
    >
      {loading && <Spinner className="h-4 w-4" />}
      {children}
    </button>
  )
}
