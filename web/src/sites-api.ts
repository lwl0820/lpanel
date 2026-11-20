import { request } from './api'
import type { Binding, BindingResult } from './domain-api'
export interface Site { id: string; name: string; directory: string; spa: boolean; updated_at: string }
export type SiteInput = Pick<Site, 'name' | 'directory' | 'spa'>
type SiteBindingInput = Pick<Binding, 'domain' | 'auto_certificate'> & { acme_account_id?: string }
type SiteCreateResult = Site & Partial<Pick<BindingResult, 'binding' | 'operation'>> & { certificate_error?: string }
export const sites = {
 list: () => request<Site[]>('/sites'),
 create: (value: SiteInput & { binding?: SiteBindingInput }) => request<SiteCreateResult>('/sites', {method: 'POST', body: JSON.stringify(value)}),
 update: (id: string, value: SiteInput) => request<Site>(`/sites/${id}`, {method: 'PUT', body: JSON.stringify(value)}),
 remove: (id: string) => request<{ok: boolean}>(`/sites/${id}`, {method: 'DELETE'}),
 bind: (id: string, value: SiteBindingInput) => request<BindingResult>(`/sites/${id}/bindings`, {method: 'POST', body: JSON.stringify(value)}),
}
