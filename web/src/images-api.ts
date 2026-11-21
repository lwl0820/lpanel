import { request } from './api'

export interface LocalImage { id: string; tags: string[]; size: string }

export const images = {
  list: () => request<LocalImage[]>('/images'),
  remove: (id: string) => request<{ message: string }>(`/images/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  prune: () => request<{ message: string }>('/images/prune', { method: 'POST' }),
}
