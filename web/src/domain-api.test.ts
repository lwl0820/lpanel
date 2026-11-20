import { expect, it } from 'vitest'
import { panelLoginURL } from './domain-api'

it('复制带 IPv6 地址的面板登录入口', () => {
  const access = { panel_port: 8443, panel_domain: '2001:db8::1', https: true, login_path: '/my-entry' }
  expect(panelLoginURL(access)).toBe('https://[2001:db8::1]:8443/my-entry')
  expect(panelLoginURL(access, '[2001:db8::2]')).toBe('https://[2001:db8::2]:8443/my-entry')
})
