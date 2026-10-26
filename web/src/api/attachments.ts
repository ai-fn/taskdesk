import axios from 'axios'
import { api } from './client'
import type { Attachment, AttachmentListResponse } from '@/types'

// Bare axios instance for presigned storage uploads: must NOT carry the
// Authorization header (it breaks the presign signature) or credentials.
const storage = axios.create()

export interface CreateUploadURLInput {
  filename: string
  content_type?: string
  size_bytes: number
}

export interface CreateUploadURLResponse {
  upload_url: string
  attachment_id: string
}

export async function createUploadURL(
  taskId: string,
  input: CreateUploadURLInput,
): Promise<CreateUploadURLResponse> {
  const { data } = await api.post<CreateUploadURLResponse>(
    `/api/v1/tasks/${taskId}/attachments/upload-url`,
    input,
  )
  return data
}

export async function putToStorage(
  url: string,
  file: Blob,
  contentType: string,
  onProgress?: (percent: number) => void,
): Promise<void> {
  await storage.put(url, file, {
    headers: { 'Content-Type': contentType },
    onUploadProgress: onProgress
      ? (ev) => {
          if (ev.total) onProgress(Math.round((ev.loaded / ev.total) * 100))
        }
      : undefined,
  })
}

export async function confirmAttachment(
  taskId: string,
  attachmentId: string,
): Promise<Attachment> {
  const { data } = await api.post<Attachment>(
    `/api/v1/tasks/${taskId}/attachments/${attachmentId}/confirm`,
  )
  return data
}

export async function listAttachments(
  taskId: string,
  pageToken?: string,
): Promise<AttachmentListResponse> {
  const params: Record<string, string> = {}
  if (pageToken) params.page_token = pageToken
  const { data } = await api.get<AttachmentListResponse>(
    `/api/v1/tasks/${taskId}/attachments`,
    { params },
  )
  return data
}

export interface GetDownloadURLResponse {
  download_url: string
}

export async function getDownloadURL(
  taskId: string,
  attachmentId: string,
): Promise<GetDownloadURLResponse> {
  const { data } = await api.get<GetDownloadURLResponse>(
    `/api/v1/tasks/${taskId}/attachments/${attachmentId}/download-url`,
  )
  return data
}

export async function deleteAttachment(
  taskId: string,
  attachmentId: string,
): Promise<void> {
  await api.delete(`/api/v1/tasks/${taskId}/attachments/${attachmentId}`)
}

// ── Multipart ──────────────────────────────────────────────────────────────

export interface InitiateMultipartInput {
  filename: string
  content_type?: string
  size_bytes: number
}

export interface InitiateMultipartResponse {
  attachment_id: string
  upload_id: string
}

export async function initiateMultipart(
  taskId: string,
  input: InitiateMultipartInput,
): Promise<InitiateMultipartResponse> {
  const { data } = await api.post<InitiateMultipartResponse>(
    `/api/v1/tasks/${taskId}/attachments/multipart/initiate`,
    input,
  )
  return data
}

export interface GetPartURLResponse {
  upload_url: string
}

export async function getPartURL(
  taskId: string,
  attachmentId: string,
  partNumber: number,
): Promise<GetPartURLResponse> {
  const { data } = await api.get<GetPartURLResponse>(
    `/api/v1/tasks/${taskId}/attachments/${attachmentId}/parts/${partNumber}`,
  )
  return data
}

export interface CompletedPart {
  part_number: number
  etag: string
}

export const MISSING_ETAG = '__missingEtag__'

// putPart uploads a single part directly to the presigned URL and returns
// the ETag from the response headers (required for CompleteMultipart).
export async function putPart(
  url: string,
  blob: Blob,
  onProgress?: (percent: number) => void,
): Promise<string> {
  const resp = await storage.put(url, blob, {
    onUploadProgress: onProgress
      ? (ev) => {
          if (ev.total) onProgress(Math.round((ev.loaded / ev.total) * 100))
        }
      : undefined,
  })
  // Absent when the bucket's CORS rules do not expose ETag; completing without it would corrupt the upload.
  const etag: string = resp.headers['etag'] ?? ''
  if (!etag) throw new Error(MISSING_ETAG)
  return etag
}

export async function completeMultipart(
  taskId: string,
  attachmentId: string,
  parts: CompletedPart[],
): Promise<Attachment> {
  const { data } = await api.post<Attachment>(
    `/api/v1/tasks/${taskId}/attachments/${attachmentId}/multipart/complete`,
    { parts },
  )
  return data
}
