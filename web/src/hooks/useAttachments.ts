import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import axios from 'axios'
import { taskKeys } from '@/api/queryKeys'
import type { AttachmentListResponse } from '@/types'
import {
  listAttachments,
  createUploadURL,
  putToStorage,
  confirmAttachment,
  initiateMultipart,
  getPartURL,
  putPart,
  completeMultipart,
  getDownloadURL,
  deleteAttachment,
} from '@/api/attachments'
import { useAuth } from '@/hooks/useAuth'

// 100 MiB — must mirror multipartThreshold in services/todo/internal/server/attachments.go
export const MULTIPART_THRESHOLD = 100 * 1024 * 1024
// 10 MiB — must mirror freeMaxFileBytes in services/todo/internal/server/attachments.go
export const FREE_MAX_FILE_BYTES = 10 * 1024 * 1024
// 16 MiB per part; only files above ~156 GiB need larger parts to stay within S3's 10000-part limit
const PART_SIZE = 16 * 1024 * 1024
const MAX_PARTS = 10_000
const MIB = 1024 * 1024

function partSizeFor(fileSize: number): number {
  return Math.max(PART_SIZE, Math.ceil(fileSize / MAX_PARTS / MIB) * MIB)
}

// Fire-and-forget: frees the reservation (and aborts a multipart upload) after a failed transfer.
// Never called once confirm/complete was sent, since the server may have committed despite a lost response.
function discardUpload(taskId: string, attachmentId: string): void {
  deleteAttachment(taskId, attachmentId).catch(() => {
    // the server reaps abandoned uploads on its own
  })
}

function extractErrorMessage(err: unknown): string {
  if (axios.isAxiosError(err)) {
    return (err.response?.data as { error?: string })?.error ?? err.message
  }
  if (err instanceof Error) return err.message
  return String(err)
}

export function useAttachments(taskId: string) {
  const result = useQuery({
    queryKey: taskKeys.attachments(taskId),
    queryFn: () => listAttachments(taskId),
    staleTime: 10_000,
    // Poll while any attachment is awaiting a scan verdict
    refetchInterval: (query) => {
      const data = query.state.data as AttachmentListResponse | undefined
      const hasPending = data?.attachments.some((a) => a.scan_status === 'pending')
      return hasPending ? 4_000 : false
    },
  })
  return result
}

export interface UploadProgressEvent {
  filename: string
  percent: number
}

export function useUploadAttachment(
  taskId: string,
  opts?: { onProgress?: (e: UploadProgressEvent) => void },
) {
  const qc = useQueryClient()
  const { plan } = useAuth()

  return useMutation({
    mutationFn: async (file: File) => {
      const contentType = file.type || 'application/octet-stream'
      const setProgress = (percent: number) =>
        opts?.onProgress?.({ filename: file.name, percent })

      if (file.size < MULTIPART_THRESHOLD) {
        // ── Single-PUT flow ───────────────────────────────────────────────
        const { upload_url, attachment_id } = await createUploadURL(taskId, {
          filename: file.name,
          content_type: contentType,
          size_bytes: file.size,
        })
        try {
          await putToStorage(upload_url, file, contentType, setProgress)
        } catch (err) {
          discardUpload(taskId, attachment_id)
          throw err
        }
        return confirmAttachment(taskId, attachment_id)
      } else {
        // ── Multipart flow (pro only) ─────────────────────────────────────
        if (plan !== 'pro') {
          throw new Error('__needsPro__')
        }
        const { attachment_id } = await initiateMultipart(taskId, {
          filename: file.name,
          content_type: contentType,
          size_bytes: file.size,
        })

        const partSize = partSizeFor(file.size)
        const totalParts = Math.ceil(file.size / partSize)
        const partBytesLoaded = new Array<number>(totalParts).fill(0)

        const updateAggregateProgress = () => {
          const loaded = partBytesLoaded.reduce((s, b) => s + b, 0)
          setProgress(Math.round((loaded / file.size) * 100))
        }

        const parts: { part_number: number; etag: string }[] = []
        try {
          for (let i = 0; i < totalParts; i++) {
            const partNumber = i + 1
            const start = i * partSize
            const end = Math.min(start + partSize, file.size)
            const blob = file.slice(start, end)

            const { upload_url: partUrl } = await getPartURL(taskId, attachment_id, partNumber)
            const etag = await putPart(partUrl, blob, (partPct) => {
              partBytesLoaded[i] = Math.round((blob.size * partPct) / 100)
              updateAggregateProgress()
            })
            parts.push({ part_number: partNumber, etag })
          }
        } catch (err) {
          discardUpload(taskId, attachment_id)
          throw err
        }
        return completeMultipart(taskId, attachment_id, parts)
      }
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: taskKeys.attachments(taskId) })
      qc.invalidateQueries({ queryKey: taskKeys.detail(taskId) })
      qc.invalidateQueries({ queryKey: taskKeys.all })
    },
    onError: () => {
      // Invalidate so counts stay accurate on partial failures
      qc.invalidateQueries({ queryKey: taskKeys.attachments(taskId) })
      qc.invalidateQueries({ queryKey: taskKeys.detail(taskId) })
    },
  })
}

export function useDeleteAttachment(taskId: string) {
  const qc = useQueryClient()

  return useMutation({
    mutationFn: (attachmentId: string) => deleteAttachment(taskId, attachmentId),
    onMutate: async (attachmentId) => {
      await qc.cancelQueries({ queryKey: taskKeys.attachments(taskId) })
      const snapshot = qc.getQueryData<AttachmentListResponse>(
        taskKeys.attachments(taskId),
      )
      qc.setQueryData<AttachmentListResponse>(taskKeys.attachments(taskId), (old) => ({
        attachments: (old?.attachments ?? []).filter((a) => a.id !== attachmentId),
        next_page_token: old?.next_page_token ?? '',
      }))
      return { snapshot }
    },
    onError: (_err, _vars, ctx) => {
      if (ctx?.snapshot)
        qc.setQueryData(taskKeys.attachments(taskId), ctx.snapshot)
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: taskKeys.attachments(taskId) })
      qc.invalidateQueries({ queryKey: taskKeys.detail(taskId) })
      qc.invalidateQueries({ queryKey: taskKeys.all })
    },
  })
}

export function useDownloadAttachment(taskId: string) {
  return useMutation({
    mutationFn: async (attachmentId: string) => {
      const { download_url } = await getDownloadURL(taskId, attachmentId)
      const a = document.createElement('a')
      a.href = download_url
      a.rel = 'noopener noreferrer'
      a.click()
    },
  })
}

export { extractErrorMessage }
