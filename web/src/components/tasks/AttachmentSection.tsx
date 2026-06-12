import { useRef, useState, useCallback, DragEvent } from 'react'
import { useTranslation } from 'react-i18next'
import axios from 'axios'
import { toBCP47 } from '@/i18n/config'
import { formatBytes } from '@/lib/format'
import { useAttachments, useUploadAttachment, useDeleteAttachment, useDownloadAttachment, MULTIPART_THRESHOLD, FREE_MAX_FILE_BYTES } from '@/hooks/useAttachments'
import { useAuth } from '@/hooks/useAuth'
import { Button } from '@/components/ui/Button'
import { Spinner } from '@/components/ui/Spinner'
import { ProgressBar } from '@/components/ui/ProgressBar'
import { ConfirmDialog } from '@/components/ui/ConfirmDialog'
import type { Attachment } from '@/types'

interface UploadState {
  filename: string
  percent: number
}

interface Props {
  taskId: string
  addToast: (text: string) => void
}

export function AttachmentSection({ taskId, addToast }: Props) {
  const { t, i18n } = useTranslation()
  const { plan } = useAuth()
  const locale = toBCP47(i18n.language)

  const { data, isLoading } = useAttachments(taskId)
  const upload = useUploadAttachment(taskId, {
    onProgress: ({ filename, percent }) =>
      setUploading((prev) =>
        prev.map((u) => (u.filename === filename ? { ...u, percent } : u)),
      ),
  })
  const del = useDeleteAttachment(taskId)
  const download = useDownloadAttachment(taskId)

  const [uploading, setUploading] = useState<UploadState[]>([])
  const [deleteTarget, setDeleteTarget] = useState<Attachment | null>(null)
  const [dragOver, setDragOver] = useState(false)
  const inputRef = useRef<HTMLInputElement>(null)

  const attachments = data?.attachments ?? []

  function validateAndQueue(files: FileList | File[]) {
    for (const file of Array.from(files)) {
      if (plan === 'free' && file.size > FREE_MAX_FILE_BYTES) {
        addToast(t('attachments.errorTooLargeFree', { max: '10 MiB' }))
        continue
      }
      if (plan !== 'pro' && file.size >= MULTIPART_THRESHOLD) {
        addToast(t('attachments.errorNeedsPro'))
        continue
      }
      setUploading((prev) => [...prev, { filename: file.name, percent: 0 }])
      upload.mutate(file, {
        onSuccess: () =>
          setUploading((prev) => prev.filter((u) => u.filename !== file.name)),
        onError: (err) => {
          setUploading((prev) => prev.filter((u) => u.filename !== file.name))
          if (axios.isAxiosError(err) && err.response?.data?.error) {
            addToast(err.response.data.error as string)
          } else if (err instanceof Error && err.message !== '__needsPro__') {
            addToast(err.message)
          } else if (err instanceof Error && err.message === '__needsPro__') {
            addToast(t('attachments.errorNeedsPro'))
          }
        },
      })
    }
  }

  function handleFileInput(e: React.ChangeEvent<HTMLInputElement>) {
    if (e.target.files) validateAndQueue(e.target.files)
    e.target.value = ''
  }

  const handleDrop = useCallback(
    (e: DragEvent<HTMLDivElement>) => {
      e.preventDefault()
      setDragOver(false)
      if (e.dataTransfer.files) validateAndQueue(e.dataTransfer.files)
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [plan],
  )

  function handleDragOver(e: DragEvent<HTMLDivElement>) {
    e.preventDefault()
    setDragOver(true)
  }
  function handleDragLeave() {
    setDragOver(false)
  }

  function confirmDelete() {
    if (!deleteTarget) return
    del.mutate(deleteTarget.id, {
      onError: () => addToast(t('common.error')),
    })
    setDeleteTarget(null)
  }

  return (
    <section className="mt-6 rounded-xl border border-gray-200 dark:border-gray-700 bg-white dark:bg-gray-800 p-6 shadow-sm">
      <div className="mb-3 flex items-center justify-between gap-3">
        <h2 className="text-sm font-semibold text-gray-700 dark:text-gray-300 uppercase tracking-wide">
          {t('attachments.title')}
        </h2>
        <Button
          variant="secondary"
          className="text-xs"
          onClick={() => inputRef.current?.click()}
        >
          {t('attachments.add')}
        </Button>
      </div>

      {/* Hidden file input */}
      <input
        ref={inputRef}
        type="file"
        multiple
        className="hidden"
        onChange={handleFileInput}
      />

      {/* Drop zone (shown when no attachments and no uploads in progress) */}
      {attachments.length === 0 && uploading.length === 0 && !isLoading && (
        <div
          onDrop={handleDrop}
          onDragOver={handleDragOver}
          onDragLeave={handleDragLeave}
          onClick={() => inputRef.current?.click()}
          className={`flex cursor-pointer flex-col items-center justify-center rounded-lg border-2 border-dashed py-8 text-sm transition-colors ${
            dragOver
              ? 'border-indigo-400 bg-indigo-50 dark:border-indigo-500 dark:bg-indigo-900/20 text-indigo-600 dark:text-indigo-300'
              : 'border-gray-200 dark:border-gray-600 text-gray-400 dark:text-gray-500 hover:border-indigo-300 dark:hover:border-indigo-600'
          }`}
        >
          <span className="text-2xl mb-1">📎</span>
          <span>{t('attachments.dropHint')}</span>
        </div>
      )}

      {/* Inline drop overlay when there are existing attachments */}
      {(attachments.length > 0 || uploading.length > 0) && (
        <div
          onDrop={handleDrop}
          onDragOver={handleDragOver}
          onDragLeave={handleDragLeave}
          className={`rounded-lg transition-colors ${dragOver ? 'outline outline-2 outline-indigo-400 dark:outline-indigo-500 bg-indigo-50/50 dark:bg-indigo-900/10' : ''}`}
        >
          {/* In-progress uploads */}
          {uploading.map((u) => (
            <div
              key={u.filename}
              className="flex items-center gap-3 py-2 border-b border-gray-100 dark:border-gray-700 last:border-0"
            >
              <span className="text-lg shrink-0">📎</span>
              <div className="flex-1 min-w-0">
                <p className="text-sm text-gray-700 dark:text-gray-300 truncate">{u.filename}</p>
                <div className="mt-1 flex items-center gap-2">
                  <ProgressBar percent={u.percent} className="flex-1" />
                  <span className="text-xs text-gray-400 dark:text-gray-500 shrink-0">
                    {u.percent}%
                  </span>
                </div>
              </div>
              <span className="text-xs text-gray-400 dark:text-gray-500 shrink-0">
                {t('attachments.uploading')}
              </span>
            </div>
          ))}

          {/* Committed attachments */}
          {isLoading ? (
            <div className="flex items-center justify-center py-4">
              <Spinner className="h-5 w-5 text-indigo-500" />
            </div>
          ) : (
            attachments.map((att) => (
              <AttachmentRow
                key={att.id}
                att={att}
                locale={locale}
                onDownload={() => download.mutate(att.id)}
                onDelete={() => setDeleteTarget(att)}
                isDownloading={download.isPending}
              />
            ))
          )}
        </div>
      )}

      {/* Loading spinner (first load, no data yet) */}
      {isLoading && attachments.length === 0 && uploading.length === 0 && (
        <div className="flex items-center justify-center py-6">
          <Spinner className="h-5 w-5 text-indigo-500" />
        </div>
      )}

      {deleteTarget && (
        <ConfirmDialog
          title={t('attachments.delete')}
          message={t('attachments.confirmDelete')}
          confirmText={t('attachments.delete')}
          variant="danger"
          isLoading={del.isPending}
          onConfirm={confirmDelete}
          onCancel={() => setDeleteTarget(null)}
        />
      )}
    </section>
  )
}

function AttachmentRow({
  att,
  locale,
  onDownload,
  onDelete,
  isDownloading,
}: {
  att: Attachment
  locale: string
  onDownload: () => void
  onDelete: () => void
  isDownloading: boolean
}) {
  const { t } = useTranslation()
  const isClean = att.scan_status === 'clean'
  const isQuarantined = att.scan_status === 'quarantined'
  const isPending = att.scan_status === 'pending'

  return (
    <div className="flex items-center gap-3 py-2 border-b border-gray-100 dark:border-gray-700 last:border-0 group">
      <span className="text-lg shrink-0">{fileIconFor(att.content_type)}</span>
      <div className="flex-1 min-w-0">
        <p className="text-sm text-gray-700 dark:text-gray-300 truncate">{att.filename}</p>
        <p className="text-xs text-gray-400 dark:text-gray-500">
          {formatBytes(att.size_bytes, locale)} ·{' '}
          {new Date(att.created_at).toLocaleDateString(locale)}
        </p>
      </div>

      {/* Scan status chip */}
      {isPending && (
        <span className="flex items-center gap-1 text-xs text-amber-600 dark:text-amber-400 shrink-0">
          <Spinner className="h-3 w-3" />
          {t('attachments.scanning')}
        </span>
      )}
      {isQuarantined && (
        <span className="text-xs text-red-600 dark:text-red-400 shrink-0">
          ✕ {t('attachments.quarantined')}
        </span>
      )}
      {isClean && (
        <span className="text-xs text-green-600 dark:text-green-400 shrink-0 opacity-0 group-hover:opacity-100 transition-opacity">
          ✓ {t('attachments.clean')}
        </span>
      )}

      {/* Actions */}
      <div className="flex shrink-0 items-center gap-1">
        <button
          disabled={!isClean || isDownloading}
          onClick={onDownload}
          className="rounded p-1 text-gray-400 dark:text-gray-500 hover:text-indigo-600 dark:hover:text-indigo-400 disabled:opacity-30 disabled:cursor-not-allowed transition-colors"
          title={t('attachments.download')}
        >
          ↓
        </button>
        <button
          onClick={onDelete}
          className="rounded p-1 text-gray-400 dark:text-gray-500 hover:text-red-500 dark:hover:text-red-400 transition-colors"
          title={t('attachments.delete')}
        >
          ✕
        </button>
      </div>
    </div>
  )
}

function fileIconFor(contentType: string): string {
  if (contentType.startsWith('image/')) return '🖼'
  if (contentType.startsWith('video/')) return '🎬'
  if (contentType.startsWith('audio/')) return '🎵'
  if (contentType.includes('pdf')) return '📄'
  if (contentType.includes('zip') || contentType.includes('tar') || contentType.includes('gzip')) return '🗜'
  if (contentType.includes('text/')) return '📝'
  return '📎'
}
