import { request } from './api'
import type { Binding, BindingResult } from './domain-api'
export interface Site { id: string; identifier: string; directory: string; spa: boolean; note: string; updated_at: string }
export type SiteInput = Pick<Site, 'identifier' | 'directory' | 'spa' | 'note'>
type SiteBindingInput = Pick<Binding, 'domain' | 'auto_certificate'> & { acme_account_id?: string }
type SiteCreateInput = SiteInput & { hostnames: string[]; auto_certificate?: boolean; acme_account_id?: string }
type SiteCreateResult = Site & Partial<Pick<BindingResult, 'operation'>> & { bindings: Binding[]; certificate_error?: string }
export const sites = {
 list: () => request<Site[]>('/sites'),
 create: (value: SiteCreateInput) => request<SiteCreateResult>('/sites', {method: 'POST', body: JSON.stringify(value)}),
 update: (id: string, value: SiteInput) => request<Site>(`/sites/${id}`, {method: 'PUT', body: JSON.stringify(value)}),
 remove: (id: string) => request<{ok: boolean}>(`/sites/${id}`, {method: 'DELETE'}),
 bind: (id: string, value: SiteBindingInput) => request<BindingResult>(`/sites/${id}/bindings`, {method: 'POST', body: JSON.stringify(value)}),
}
