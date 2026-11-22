import { request, type Operation } from './api'
export interface UpgradeState { repository: string; current: string; latest: string; available: boolean; checked_at: string; error: string; auto_update: boolean }
export const upgrades = {
  status: () => request<UpgradeState>('/system/updates'),
  check: () => request<UpgradeState>('/system/updates/check', { method: 'POST' }),
  repository: (repository: string) => request<UpgradeState>('/system/updates/repository', { method: 'PUT', body: JSON.stringify({ repository }) }),
  setAutoUpdate: (auto_update: boolean) => request<UpgradeState>('/system/updates/auto-update', { method: 'PUT', body: JSON.stringify({ auto_update }) }),
  install: () => request<Operation>('/system/updates/upgrade', { method: 'POST' }),
  operation: (id: string) => request<Operation>(`/operations/${id}`),
}
