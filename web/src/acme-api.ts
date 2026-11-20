import { request } from './api'

export interface ACMEProvider {
  id: string
  name: string
  tos_url: string
  requires_eab: boolean
}

export interface ACMEAccount {
  id: string
  name: string
  provider: string
  provider_name: string
  email: string
  agree_tos: boolean
  needs_eab?: boolean
}

export interface ACMEAccountList {
  accounts: ACMEAccount[]
  default_account_id: string | null
  providers: ACMEProvider[]
}

export type ACMEAccountInput = Pick<ACMEAccount, 'name' | 'provider' | 'email' | 'agree_tos'> & {
  eab_kid?: string
  eab_hmac_key?: string
}

export const acme = {
  list: () => request<ACMEAccountList>('/acme/accounts'),
  save: (id: string, account: ACMEAccountInput) => request<ACMEAccount>(id ? `/acme/accounts/${id}` : '/acme/accounts', {
    method: id ? 'PUT' : 'POST',
    body: JSON.stringify(account),
  }),
  remove: (id: string) => request<{ ok: boolean }>(`/acme/accounts/${id}`, { method: 'DELETE' }),
}
