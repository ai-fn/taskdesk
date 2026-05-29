import { useCallback, useState } from 'react'
import { Button } from '@/components/ui/Button'
import { Spinner } from '@/components/ui/Spinner'
import { ConfirmDialog } from '@/components/ui/ConfirmDialog'
import { ToastContainer, type ToastMessage } from '@/components/ui/Toast'
import { useSessions, useRevokeSession, useRevokeOtherSessions } from '@/hooks/useSessions'

type Confirm =
  | { type: 'revoke'; sessionId: string }
  | { type: 'revoke-others' }
  | null

export function AccountPage() {
  const { data: sessions = [], isLoading } = useSessions()
  const revokeSession = useRevokeSession()
  const revokeOthers = useRevokeOtherSessions()
  const [confirm, setConfirm] = useState<Confirm>(null)
  const [toasts, setToasts] = useState<ToastMessage[]>([])

  const addToast = (text: string) =>
    setToasts((prev) => [...prev, { id: Date.now(), text }])

  const dismissToast = useCallback((id: number) => {
    setToasts((prev) => prev.filter((t) => t.id !== id))
  }, [])

  function handleConfirm() {
    if (!confirm) return
    if (confirm.type === 'revoke') {
      revokeSession.mutate(confirm.sessionId, {
        onSuccess: () => addToast('Session signed out'),
        onError: () => addToast('Failed to sign out session'),
      })
    } else {
      revokeOthers.mutate(undefined, {
        onSuccess: (n) => addToast(`Signed out ${n} other ${n === 1 ? 'session' : 'sessions'}`),
        onError: () => addToast('Failed to sign out other sessions'),
      })
    }
    setConfirm(null)
  }

  const isPending = revokeSession.isPending || revokeOthers.isPending

  return (
    <div className="space-y-8">
      <h1 className="text-2xl font-bold text-gray-900 dark:text-gray-100">Account</h1>

      <section className="space-y-4">
        <div className="flex items-center justify-between">
          <div>
            <h2 className="text-lg font-semibold text-gray-900 dark:text-gray-100">Active sessions</h2>
            <p className="text-sm text-gray-500 dark:text-gray-400">
              Devices currently signed in to your account
            </p>
          </div>
          {sessions.length > 1 && (
            <Button
              variant="danger"
              onClick={() => setConfirm({ type: 'revoke-others' })}
              disabled={isPending}
            >
              Sign out all other devices
            </Button>
          )}
        </div>

        {isLoading ? (
          <div className="flex justify-center py-8">
            <Spinner className="h-6 w-6 text-indigo-500" />
          </div>
        ) : sessions.length === 0 ? (
          <p className="text-sm text-gray-500 dark:text-gray-400">No active sessions found.</p>
        ) : (
          <div className="space-y-2">
            {sessions.map((session) => (
              <div
                key={session.id}
                className="rounded-xl bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-800 p-4 shadow-sm"
              >
                <div className="flex items-start justify-between gap-4">
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-2 flex-wrap">
                      <span className="font-medium text-gray-900 dark:text-gray-100 truncate">
                        {session.browser || 'Unknown browser'}
                        {session.os ? ` on ${session.os}` : ''}
                      </span>
                      <span className="text-xs text-gray-500 dark:text-gray-400">
                        {session.device}
                      </span>
                      {session.current && (
                        <span className="inline-flex items-center rounded-full bg-indigo-100 dark:bg-indigo-900 px-2 py-0.5 text-xs font-medium text-indigo-700 dark:text-indigo-300">
                          Current
                        </span>
                      )}
                    </div>
                    <div className="mt-1 flex flex-wrap gap-x-4 gap-y-1 text-xs text-gray-500 dark:text-gray-400">
                      {session.ip_address && <span>IP: {session.ip_address}</span>}
                      <span>
                        Last active:{' '}
                        {new Date(session.last_active_at).toLocaleString(undefined, {
                          dateStyle: 'medium',
                          timeStyle: 'short',
                        })}
                      </span>
                      <span>
                        Signed in:{' '}
                        {new Date(session.created_at).toLocaleString(undefined, {
                          dateStyle: 'medium',
                          timeStyle: 'short',
                        })}
                      </span>
                    </div>
                  </div>
                  {!session.current && (
                    <Button
                      variant="ghost"
                      className="shrink-0 text-red-600 dark:text-red-400 hover:bg-red-50 dark:hover:bg-red-900/30"
                      onClick={() => setConfirm({ type: 'revoke', sessionId: session.id })}
                      disabled={isPending}
                    >
                      Sign out
                    </Button>
                  )}
                </div>
              </div>
            ))}
          </div>
        )}
      </section>

      {confirm && (
        <ConfirmDialog
          title={confirm.type === 'revoke' ? 'Sign out session' : 'Sign out all other devices'}
          message={
            confirm.type === 'revoke'
              ? 'This session will be signed out immediately. The device will need to log in again.'
              : 'All other sessions will be signed out immediately. Only your current session will remain active.'
          }
          confirmText={confirm.type === 'revoke' ? 'Sign out' : 'Sign out all others'}
          variant="danger"
          isLoading={isPending}
          onConfirm={handleConfirm}
          onCancel={() => setConfirm(null)}
        />
      )}

      <ToastContainer toasts={toasts} onDismiss={dismissToast} />
    </div>
  )
}
