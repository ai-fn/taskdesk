import { useRef } from 'react'

interface OTPInputProps {
  length?: number
  value: string
  onChange: (value: string) => void
  autoFocus?: boolean
  disabled?: boolean
  error?: boolean
}

export function OTPInput({ length = 6, value, onChange, autoFocus, disabled, error }: OTPInputProps) {
  const refs = useRef<(HTMLInputElement | null)[]>([])
  const digits = Array.from({ length }, (_, i) => value[i] ?? '')

  function focusAt(i: number) {
    refs.current[Math.max(0, Math.min(i, length - 1))]?.focus()
  }

  function handleChange(index: number, raw: string) {
    const digit = raw.replace(/\D/g, '').slice(-1)
    if (!digit) return
    const next = digits.slice()
    next[index] = digit
    onChange(next.join(''))
    if (index < length - 1) focusAt(index + 1)
  }

  function handleKeyDown(index: number, e: React.KeyboardEvent<HTMLInputElement>) {
    if (e.key === 'Backspace') {
      e.preventDefault()
      const next = digits.slice()
      if (digits[index]) {
        next[index] = ''
        onChange(next.join(''))
      } else if (index > 0) {
        next[index - 1] = ''
        onChange(next.join(''))
        focusAt(index - 1)
      }
    } else if (e.key === 'ArrowLeft') {
      e.preventDefault()
      focusAt(index - 1)
    } else if (e.key === 'ArrowRight') {
      e.preventDefault()
      focusAt(index + 1)
    }
  }

  function handlePaste(e: React.ClipboardEvent) {
    e.preventDefault()
    const pasted = e.clipboardData.getData('text').replace(/\D/g, '').slice(0, length)
    if (!pasted) return
    const next = Array.from({ length }, (_, i) => pasted[i] ?? '')
    onChange(next.join(''))
    focusAt(Math.min(pasted.length, length - 1))
  }

  return (
    <div className="flex gap-2 justify-center" onPaste={handlePaste}>
      {digits.map((digit, i) => (
        <input
          key={i}
          ref={el => { refs.current[i] = el }}
          type="text"
          inputMode="numeric"
          pattern="[0-9]*"
          autoComplete={i === 0 ? 'one-time-code' : 'off'}
          maxLength={1}
          value={digit}
          autoFocus={autoFocus && i === 0}
          disabled={disabled}
          onChange={e => handleChange(i, e.target.value)}
          onKeyDown={e => handleKeyDown(i, e)}
          onFocus={e => e.target.select()}
          className={[
            'w-11 h-14 rounded-xl border-2 text-center text-xl font-bold font-mono',
            'outline-none transition-all duration-150 select-none',
            'bg-white dark:bg-gray-800',
            'focus:ring-2 focus:ring-offset-0',
            disabled ? 'opacity-40 cursor-not-allowed' : '',
            error
              ? 'border-red-400 dark:border-red-500 text-red-600 dark:text-red-400 focus:border-red-500 focus:ring-red-500/20'
              : digit
                ? 'border-indigo-500 dark:border-indigo-400 text-gray-900 dark:text-gray-100 bg-indigo-50/60 dark:bg-indigo-950/40 focus:border-indigo-600 focus:ring-indigo-500/20'
                : 'border-gray-200 dark:border-gray-700 text-gray-900 dark:text-gray-100 focus:border-indigo-500 dark:focus:border-indigo-400 focus:ring-indigo-500/20',
          ].join(' ')}
        />
      ))}
    </div>
  )
}
