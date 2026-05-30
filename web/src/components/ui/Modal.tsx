import { useState } from 'react'
import type { ReactNode } from 'react'
import { useEscapeKey } from '@/hooks/useEscapeKey'

interface Props {
  onClose: () => void
  children: ReactNode
  className?: string
}

export function Modal({ onClose, children, className = '' }: Props) {
  const [isClosing, setIsClosing] = useState(false)

  function handleClose() {
    if (isClosing) return
    setIsClosing(true)
    setTimeout(onClose, 200)
  }

  useEscapeKey(handleClose)

  return (
    <div
      className={`fixed inset-0 z-50 flex items-center justify-center bg-black/40 dark:bg-black/60 px-4 ${isClosing ? 'animate-backdrop-out' : 'animate-backdrop-in'}`}
      onPointerDown={handleClose}
    >
      <div
        className={`w-full ${isClosing ? 'animate-modal-out' : 'animate-modal-in'} ${className}`}
        onPointerDown={(e) => e.stopPropagation()}
      >
        {children}
      </div>
    </div>
  )
}
