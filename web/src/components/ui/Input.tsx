import { forwardRef } from 'react'
import type { InputHTMLAttributes } from 'react'

interface Props extends InputHTMLAttributes<HTMLInputElement> {
  label?: string
  error?: string
}

export const Input = forwardRef<HTMLInputElement, Props>(({ label, error, className = '', ...props }, ref) => (
  <div className="flex flex-col gap-1">
    {label && <label className="text-sm font-medium text-gray-700">{label}</label>}
    <input
      ref={ref}
      className={`rounded-md border px-3 py-2 text-sm shadow-sm focus:border-indigo-500 focus:outline-none focus:ring-1 focus:ring-indigo-500 ${
        error ? 'border-red-400' : 'border-gray-300'
      } ${className}`}
      {...props}
    />
    {error && <p className="text-xs text-red-500">{error}</p>}
  </div>
))
Input.displayName = 'Input'
