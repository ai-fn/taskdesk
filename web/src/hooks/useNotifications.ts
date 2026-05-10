import { useEffect, useRef } from 'react'
import { getToken } from '@/api/client'

export interface TaskNotification {
  event_type: string
  task_title: string
  user_id: string
  occurred_at: string
}

type ToastFn = (n: TaskNotification) => void

// useNotifications opens a WebSocket to /api/v1/ws and calls onToast on
// every received event. Reconnects automatically on close.
export function useNotifications(onToast: ToastFn) {
  const onToastRef = useRef(onToast)
  onToastRef.current = onToast

  useEffect(() => {
    let ws: WebSocket | null = null
    let stopped = false

    function connect() {
      if (stopped) return

      const token = getToken()
      if (!token) return

      const proto = window.location.protocol === 'https:' ? 'wss' : 'ws'
      const url = `${proto}://${window.location.host}/api/v1/ws?token=${encodeURIComponent(token)}`
      ws = new WebSocket(url)

      ws.onmessage = (e) => {
        try {
          const n = JSON.parse(e.data) as TaskNotification
          onToastRef.current(n)
        } catch {
          // ignore unparseable messages
        }
      }

      ws.onclose = () => {
        if (!stopped) setTimeout(connect, 3000)
      }

      ws.onerror = () => {
        ws?.close()
      }
    }

    connect()
    return () => {
      stopped = true
      ws?.close()
    }
  }, []) // only mount/unmount — token changes handled via ref
}
