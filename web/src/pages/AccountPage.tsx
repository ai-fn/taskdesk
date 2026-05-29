import { useCallback, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/Button'
import { Spinner } from '@/components/ui/Spinner'
import { ConfirmDialog } from '@/components/ui/ConfirmDialog'
import { ToastContainer, type ToastMessage } from '@/components/ui/Toast'
import { LanguageSwitcher } from '@/components/account/LanguageSwitcher'
import { useSessions, useRevokeSession, useRevokeOtherSessions } from '@/hooks/useSessions'
import { toBCP47 } from '@/i18n/config'

type Confirm =
  | { type: 'revoke'; sessionId: string }
  | { type: 'revoke-others' }
  | null

export function AccountPage() {
  const { t, i18n } = useTranslation()
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

  const locale = toBCP47(i18n.language)

  function handleConfirm() {
    if (!confirm) return
    if (confirm.type === 'revoke') {
      revokeSession.mutate(confirm.sessionId, {
        onSuccess: () => addToast(t('account.sessions.revokeSuccess')),
        onError: () => addToast(t('account.sessions.revokeFailed')),
      })
    } else {
      revokeOthers.mutate(undefined, {
        onSuccess: (n) => addToast(t('account.sessions.signedOutOthers', { count: n })),
        onError: () => addToast(t('account.sessions.revokeOthersFailed')),
      })
    }
    setConfirm(null)
  }

  const isPending = revokeSession.isPending || revokeOthers.isPending

  return (
    <div className="space-y-8">
      <h1 className="text-2xl font-bold text-gray-900 dark:text-gray-100">{t('account.title')}</h1>

      {/* Language */}
      <section className="space-y-3">
        <h2 className="text-lg font-semibold text-gray-900 dark:text-gray-100">{t('account.language.title')}</h2>
        <LanguageSwitcher />
      </section>

      {/* Sessions */}
      <section className="space-y-4">
        <div className="flex items-center justify-between">
          <div>
            <h2 className="text-lg font-semibold text-gray-900 dark:text-gray-100">{t('account.sessions.title')}</h2>
            <p className="text-sm text-gray-500 dark:text-gray-400">
              {t('account.sessions.description')}
            </p>
          </div>
          {sessions.length > 1 && (
            <Button
              variant="danger"
              onClick={() => setConfirm({ type: 'revoke-others' })}
              disabled={isPending}
            >
              {t('account.sessions.revokeOthers')}
            </Button>
          )}
        </div>

        {isLoading ? (
          <div className="flex justify-center py-8">
            <Spinner className="h-6 w-6 text-indigo-500" />
          </div>
        ) : sessions.length === 0 ? (
          <p className="text-sm text-gray-500 dark:text-gray-400">{t('account.sessions.empty')}</p>
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
                        {session.browser || t('account.sessions.unknownBrowser')}
                        {session.os ? ` ${session.os}` : ''}
                      </span>
                      <span className="text-xs text-gray-500 dark:text-gray-400">
                        {t(`account.sessions.device.${session.device.toLowerCase()}`, { defaultValue: session.device })}
                      </span>
                      {session.current && (
                        <span className="inline-flex items-center rounded-full bg-indigo-100 dark:bg-indigo-900 px-2 py-0.5 text-xs font-medium text-indigo-700 dark:text-indigo-300">
                          {t('account.sessions.current')}
                        </span>
                      )}
                    </div>
                    <div className="mt-1 flex flex-wrap gap-x-4 gap-y-1 text-xs text-gray-500 dark:text-gray-400">
                      {session.ip_address && (
                        <span>{t('account.sessions.ip')} {session.ip_address}</span>
                      )}
                      <span>
                        {t('account.sessions.lastActive')}{' '}
                        {new Date(session.last_active_at).toLocaleString(locale, {
                          dateStyle: 'medium',
                          timeStyle: 'short',
                        })}
                      </span>
                      <span>
                        {t('account.sessions.signedIn')}{' '}
                        {new Date(session.created_at).toLocaleString(locale, {
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
                      {t('account.sessions.revoke')}
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
          title={confirm.type === 'revoke'
            ? t('account.sessions.revokeConfirm')
            : t('account.sessions.revokeOthersConfirm')}
          message={confirm.type === 'revoke'
            ? t('account.sessions.revokeBody')
            : t('account.sessions.revokeOthersBody')}
          confirmText={confirm.type === 'revoke'
            ? t('account.sessions.revoke')
            : t('account.sessions.revokeOthers')}
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
