import { useEffect, useRef } from 'react'

// Module-level stack so nested modals close one at a time.
// The topmost entry fires on Escape; outer ones are shielded.
const stack: Array<React.MutableRefObject<() => void>> = []

function handleKeyDown(e: KeyboardEvent) {
  if (e.key === 'Escape' && stack.length > 0) {
    e.stopImmediatePropagation()
    stack[stack.length - 1].current()
  }
}

export function useEscapeKey(onClose: () => void) {
  const ref = useRef(onClose)
  ref.current = onClose

  useEffect(() => {
    if (stack.length === 0) {
      document.addEventListener('keydown', handleKeyDown)
    }
    stack.push(ref)
    return () => {
      const idx = stack.indexOf(ref)
      if (idx !== -1) stack.splice(idx, 1)
      if (stack.length === 0) {
        document.removeEventListener('keydown', handleKeyDown)
      }
    }
  }, [])
}
