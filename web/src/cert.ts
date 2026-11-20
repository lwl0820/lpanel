import type { Binding } from './domain-api'
import type { UpgradeState } from './upgrade-api'
export type CertState = 'valid' | 'expiring' | 'requesting' | 'failed' | 'disabled'
export interface Todo { kind: 'cert-failed' | 'cert-expiring' | 'upgrade'; binding?: Binding; days?: number }
const DAY = 24 * 60 * 60 * 1000
export const EXPIRING_DAYS = 30
export function certificateActionLabel(binding: Binding): string {
  if (binding.certificate_status === 'enabled') return '续期证书'
  if (binding.certificate_status === 'failed') return '重新申请证书'
  return '申请证书'
}
export function daysLeft(expiresAt: string, now: Date): number | null {
  if (!expiresAt) return null
  const time = new Date(expiresAt).getTime()
  if (Number.isNaN(time)) return null
  return Math.floor((time - now.getTime()) / DAY)
}
export function certState(b: Binding, now: Date): CertState {
  if (b.certificate_status === 'requesting') return 'requesting'
  if (b.certificate_status === 'failed') return 'failed'
  if (b.certificate_status !== 'enabled') return 'disabled'
  const days = daysLeft(b.expires_at, now)
  return days !== null && days <= EXPIRING_DAYS ? 'expiring' : 'valid'
}
export function overviewTodos(bindings: Binding[], upgrade: UpgradeState, now: Date): Todo[] {
  const failed: Todo[] = bindings.filter(b => certState(b, now) === 'failed').map(binding => ({ kind: 'cert-failed', binding }))
  const expiring: Todo[] = bindings.filter(b => certState(b, now) === 'expiring').map((binding): Todo => ({ kind: 'cert-expiring', binding, days: daysLeft(binding.expires_at, now) ?? 0 })).sort((a, b) => (a.days ?? 0) - (b.days ?? 0))
  return [...failed, ...expiring, ...(upgrade.available ? [{ kind: 'upgrade' } as Todo] : [])]
}
