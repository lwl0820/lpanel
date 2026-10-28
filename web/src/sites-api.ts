import { request } from './api'
import type { BindingResult } from './domain-api'
export interface Site { id: string; name: string; directory: string; spa: boolean; updated_at: string }
export type SiteInput = Pick<Site, 'name' | 'directory' | 'spa'>
export const sites = {
 list: () => request<Site[]>('/sites'),
 save: (id: string, value: SiteInput) => request<Site>(id ? `/sites/${id}` : '/sites', {method: id ? 'PUT' : 'POST', body: JSON.stringify(value)}),
 remove: (id: string) => request<{ok: boolean}>(`/sites/${id}`, {method: 'DELETE'}),
 bind: (id: string, value: {domain: string; auto_certificate: boolean}) => request<BindingResult>(`/sites/${id}/bindings`, {method: 'POST', body: JSON.stringify(value)}),
}
