import { request, type Operation } from './api'

export interface SwapEntry {
  path: string
  type: 'file' | 'partition' | 'unknown'
  size_bytes: number | null
  used_bytes: number
  active: boolean
  managed: boolean
}

export interface SwapStatus {
  total_bytes: number
  used_bytes: number
  entries: SwapEntry[]
}

const prefix = '/server/swap'

export const serverSwap = {
  status: () => request<SwapStatus>(prefix),
  create: (size_mib: number) => request<Operation>(`${prefix}/files`, {
    method: 'POST', body: JSON.stringify({ size_mib }),
  }),
  resize: (path: string, size_mib: number) => request<Operation>(`${prefix}/resize`, {
    method: 'POST', body: JSON.stringify({ path, size_mib }),
  }),
  setActive: (path: string, active: boolean) => request<Operation>(`${prefix}/${active ? 'enable' : 'disable'}`, {
    method: 'POST', body: JSON.stringify({ path }),
  }),
  remove: (path: string) => request<Operation>(`${prefix}/files?path=${encodeURIComponent(path)}`, { method: 'DELETE' }),
  operation: (id: string) => request<Operation>(`/operations/${id}`),
}
