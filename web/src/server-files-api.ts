import { request } from './api'

export interface ServerFileEntry {
  name: string
  path: string
  kind: 'dir' | 'file' | 'link' | 'other'
  size: number
  modified_at: string
}

export interface ServerDirectory {
  path: string
  entries: ServerFileEntry[]
  skipped_names: number
}

const prefix = '/server/files'
const atPath = (action: string, path: string) => `${prefix}${action}?${new URLSearchParams({ path })}`
type WriteResult = { ok: true }

export const serverFiles = {
  list: (path: string) => request<ServerDirectory>(atPath('/list', path)),
  downloadURL: (path: string) => `/api/v1${atPath('/download', path)}`,
  text: (path: string) => request<{ text: string }>(atPath('/text', path)),
  saveText: (path: string, text: string) => request<WriteResult>(atPath('/text', path), { method: 'PUT', body: JSON.stringify({ text }) }),
  upload: (path: string, file: File) => request<WriteResult>(atPath('/upload', path), { method: 'PUT', body: file, headers: { 'Content-Type': 'application/octet-stream' } }),
  mkdir: (path: string) => request<WriteResult>(`${prefix}/mkdir`, { method: 'POST', body: JSON.stringify({ path }) }),
  rename: (path: string, name: string) => request<WriteResult>(`${prefix}/rename`, { method: 'POST', body: JSON.stringify({ path, name }) }),
  remove: (path: string) => request<WriteResult>(atPath('', path), { method: 'DELETE' }),
}
