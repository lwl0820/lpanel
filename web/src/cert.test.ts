import { describe, expect, test } from 'vitest'
import { certState, daysLeft, overviewTodos } from './cert'
import type { Binding } from './domain-api'
import type { UpgradeState } from './upgrade-api'
const now = new Date('2025-06-01T12:00:00Z')
const at = (days: number) => new Date(now.getTime() + days * 86400000).toISOString()
const binding = (patch: Partial<Binding> = {}): Binding => ({ id: 'b1', kind: 'site', resource_id: 's1', domain: 'a.example.com', upstream: '', directory: '', spa: false, service: '', container_port: 0, auto_certificate: true, certificate_status: 'enabled', certificate_error: '', expires_at: at(60), ...patch })
const upgrade = (available: boolean): UpgradeState => ({ repository: '', current: 'v1', latest: available ? 'v2' : 'v1', available, checked_at: '', error: '' })
describe('daysLeft', () => {
  test('空值和无效日期返回 null', () => {
    expect(daysLeft('', now)).toBeNull()
    expect(daysLeft('not-a-date', now)).toBeNull()
  })
  test('当天到期为 0，不足一天向下取整', () => {
    expect(daysLeft(now.toISOString(), now)).toBe(0)
    expect(daysLeft(at(0.5), now)).toBe(0)
    expect(daysLeft(at(10.9), now)).toBe(10)
  })
  test('已过期为负数', () => expect(daysLeft(at(-2), now)).toBe(-2))
})
describe('certState', () => {
  test('5 种状态', () => {
    expect(certState(binding(), now)).toBe('valid')
    expect(certState(binding({ expires_at: at(5) }), now)).toBe('expiring')
    expect(certState(binding({ certificate_status: 'requesting' }), now)).toBe('requesting')
    expect(certState(binding({ certificate_status: 'failed' }), now)).toBe('failed')
    expect(certState(binding({ certificate_status: 'disabled' }), now)).toBe('disabled')
  })
  test('30 天边界', () => {
    expect(certState(binding({ expires_at: at(30) }), now)).toBe('expiring')
    expect(certState(binding({ expires_at: at(31) }), now)).toBe('valid')
  })
  test('已启用但没有到期时间视为有效', () => expect(certState(binding({ expires_at: '' }), now)).toBe('valid'))
})
describe('overviewTodos', () => {
  test('失败在前，即将到期按剩余天数排序，最后是新版本', () => {
    const failed = binding({ id: 'f', certificate_status: 'failed' })
    const soon = binding({ id: 's', expires_at: at(3) })
    const later = binding({ id: 'l', expires_at: at(20) })
    expect(overviewTodos([later, binding(), failed, soon], upgrade(true), now)).toEqual([
      { kind: 'cert-failed', binding: failed },
      { kind: 'cert-expiring', binding: soon, days: 3 },
      { kind: 'cert-expiring', binding: later, days: 20 },
      { kind: 'upgrade' },
    ])
  })
  test('没有待处理事项', () => expect(overviewTodos([binding(), binding({ certificate_status: 'disabled' })], upgrade(false), now)).toEqual([]))
})
