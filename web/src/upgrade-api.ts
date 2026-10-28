import { request, type Operation } from './api'
export interface UpgradeState { repository: string; current: string; latest: string; available: boolean; checked_at: string; error: string }
export const upgrades = {
  status: () => request<UpgradeState>('/system/updates'),
  check: () => request<UpgradeState>('/system/updates/check', { method: 'POST' }),
  repository: (repository: string) => request<UpgradeState>('/system/updates/repository', { method: 'PUT', body: JSON.stringify({ repository }) }),
  install: () => request<Operation>('/system/updates/upgrade', { method: 'POST' }),
  operation: (id: string) => request<Operation>(`/operations/${id}`),
}
