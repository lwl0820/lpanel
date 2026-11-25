import { request } from './api'

export interface Program {
  id: string
  name: string
  directory: string
  entry: string
  args: string
  run_user: string
  status: 'running' | 'stopped' | 'starting' | 'stopping' | 'failed' | 'unknown'
  status_message: string
  pid: number
}

export interface ProgramPort { address: string; port: number }
export interface ProgramRuntime {
  pid: number
  cpu_percent: number | null
  memory_bytes: number | null
  connections: number | null
  ports: ProgramPort[]
  message: string
}

export type ProgramSettings = Pick<Program, 'name' | 'entry' | 'args' | 'run_user'>
export type ProgramAction = 'start' | 'stop' | 'restart'

const path = (id: string) => `/programs/${encodeURIComponent(id)}`

export const programs = {
  list: () => request<{ programs: Program[]; dependency_error: string }>('/programs'),
  create: (name: string) => request<Program>('/programs', { method: 'POST', body: JSON.stringify({ name }) }),
  save: (id: string, settings: ProgramSettings) => request<Program>(path(id), { method: 'PUT', body: JSON.stringify(settings) }),
  action: (id: string, action: ProgramAction) => request<Program>(`${path(id)}/actions/${action}`, { method: 'POST' }),
  copy: (id: string, name: string) => request<{ program: Program; skipped_links: number }>(`${path(id)}/copy`, { method: 'POST', body: JSON.stringify({ name }) }),
  remove: (id: string, removeFiles: boolean) => request<unknown>(`${path(id)}${removeFiles ? '?remove_files=true' : ''}`, { method: 'DELETE' }),
  runtime: (id: string) => request<ProgramRuntime>(`${path(id)}/runtime`),
  logsURL: (id: string) => `/api/v1${path(id)}/logs/stream`,
}
