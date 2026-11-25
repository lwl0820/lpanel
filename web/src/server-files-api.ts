import { request } from './api'

export interface ServerFileEntry {
  name: string
  path: string
  kind: 'dir' | 'file' | 'link' | 'other'
  size: number
  modified_at: string
  mode: string
  owner: string
}

export interface ServerDirectory {
  path: string
  entries: ServerFileEntry[]
  skipped_names: number
}

const prefix = '/server/files'
const atPath = (action: string, path: string) => `${prefix}${action}?${new URLSearchParams({ path })}`
const uploadPath = (id: string) => `${prefix}/uploads/${encodeURIComponent(id)}`
type WriteResult = { ok: true }

export const serverFiles = {
  list: (path: string) => request<ServerDirectory>(atPath('/list', path)),
  downloadURL: (path: string) => `/api/v1${atPath('/download', path)}`,
  text: (path: string) => request<{ text: string }>(atPath('/text', path)),
  saveText: (path: string, text: string) => request<WriteResult>(atPath('/text', path), { method: 'PUT', body: JSON.stringify({ text }) }),
  upload: (path: string, file: File) => request<WriteResult>(atPath('/upload', path), { method: 'PUT', body: file, headers: { 'Content-Type': 'application/octet-stream' } }),
  createUpload: (path: string, size: number) => request<{ id: string; chunk_size: number }>(`${prefix}/uploads`, { method: 'POST', body: JSON.stringify({ path, size }) }),
  uploadChunk: (id: string, offset: number, body: Blob) => request<{ received: number }>(`${uploadPath(id)}?${new URLSearchParams({ offset: String(offset) })}`, { method: 'PUT', body, headers: { 'Content-Type': 'application/octet-stream' } }),
  completeUpload: (id: string) => request<WriteResult>(`${uploadPath(id)}/complete`, { method: 'POST' }),
  abortUpload: (id: string) => request<WriteResult>(uploadPath(id), { method: 'DELETE' }),
  copy: (path: string, directory: string) => request<WriteResult>(`${prefix}/copy`, { method: 'POST', body: JSON.stringify({ path, directory }) }),
  move: (path: string, directory: string) => request<WriteResult>(`${prefix}/move`, { method: 'POST', body: JSON.stringify({ path, directory }) }),
  compress: (directory: string, names: string[], name: string) => request<WriteResult>(`${prefix}/compress`, { method: 'POST', body: JSON.stringify({ directory, names, name }) }),
  extract: (path: string, directory: string) => request<WriteResult>(`${prefix}/extract`, { method: 'POST', body: JSON.stringify({ path, directory }) }),
  attributes: (path: string, attributes: { mode?: string; owner?: string; recursive?: boolean }) => request<WriteResult>(`${prefix}/attributes`, { method: 'POST', body: JSON.stringify({ path, ...attributes }) }),
  mkdir: (path: string) => request<WriteResult>(`${prefix}/mkdir`, { method: 'POST', body: JSON.stringify({ path }) }),
  rename: (path: string, name: string) => request<WriteResult>(`${prefix}/rename`, { method: 'POST', body: JSON.stringify({ path, name }) }),
  remove: (path: string) => request<WriteResult>(atPath('', path), { method: 'DELETE' }),
}
