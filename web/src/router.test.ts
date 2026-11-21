import { expect, test } from 'vitest'
import { createMemoryHistory } from 'vue-router'
import { createAppRouter, pageTitle } from './router'
async function open(path: string) {
  const router = createAppRouter(createMemoryHistory())
  await router.push(path)
  return router.currentRoute.value
}
test('未知路径重定向到概览', async () => {
  expect((await open('/no-such-page')).path).toBe('/')
  expect((await open('/projects/x/y')).path).toBe('/')
})
test('专用登录入口保留浏览器路径', async () => {
  const router = createAppRouter(createMemoryHistory(), '/private/login')
  await router.push('/private/login?from=bookmark')
  expect(router.currentRoute.value.fullPath).toBe('/private/login?from=bookmark')
  expect(router.currentRoute.value.matched).toHaveLength(1)
})
test('服务器管理默认打开文件管理', async () => {
  const route = await open('/server')
  expect(route.path).toBe('/server/files')
  expect(pageTitle(route)).toBe('文件管理 · LPanel')
})
test('防火墙可从独立地址打开', async () => {
  const route = await open('/server/firewall')
  expect(route.path).toBe('/server/firewall')
  expect(pageTitle(route)).toBe('防火墙 · LPanel')
})
test('已注册页面显示对应标题', async () => {
  const titles: Record<string, string> = {
    '/': '概览', '/projects': '容器项目', '/sites': '静态网站', '/domains': '域名与证书',
    '/domains/acme-accounts': 'ACME 账户管理', '/settings': '面板设置',
    '/server/monitor': '服务器监控', '/server/processes': '实时进程',
  }
  for (const [path, title] of Object.entries(titles)) {
    const route = await open(path)
    expect(route.path).toBe(path)
    expect(pageTitle(route)).toBe(`${title} · LPanel`)
  }
})

test('交换空间可从独立地址打开', async () => {
  const route = await open('/server/swap')
  expect(route.path).toBe('/server/swap')
  expect(pageTitle(route)).toBe('交换空间 · LPanel')
})

test('时区与时间同步可从独立地址打开', async () => {
  const route = await open('/server/time')
  expect(route.path).toBe('/server/time')
  expect(pageTitle(route)).toBe('时区与时间同步 · LPanel')
})

test('DNS 设置可从独立地址打开', async () => {
  const route = await open('/server/dns')
  expect(route.path).toBe('/server/dns')
  expect(pageTitle(route)).toBe('DNS 设置 · LPanel')
})
