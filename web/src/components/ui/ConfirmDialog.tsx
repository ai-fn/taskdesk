import { Modal } from './Modal'
import { Button } from './Button'

interface Props {
  title: string
  message: string
  confirmText?: string
  variant?: 'primary' | 'danger'
  isLoading?: boolean
  onConfirm: () => void
  onCancel: () => void
}

export function ConfirmDialog({
  title,
  message,
  confirmText = 'Confirm',
  variant = 'primary',
  isLoading,
  onConfirm,
  onCancel,
}: Props) {
  return (
    <Modal onClose={onCancel} className="max-w-sm">
      <div className="rounded-2xl bg-white dark:bg-gray-800 p-6 shadow-xl">
        <h2 className="text-lg font-semibold text-gray-900 dark:text-gray-100 mb-2">{title}</h2>
        <p className="text-sm text-gray-600 dark:text-gray-300 mb-6">{message}</p>
        <div className="flex justify-end gap-2">
          <Button variant="secondary" onClick={onCancel} disabled={isLoading}>
            Cancel
          </Button>
          <Button variant={variant} loading={isLoading} onClick={onConfirm}>
            {confirmText}
          </Button>
        </div>
      </div>
    </Modal>
  )
}
