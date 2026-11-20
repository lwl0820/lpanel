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
test('已注册页面显示对应标题', async () => {
  const titles: Record<string, string> = { '/': '概览', '/projects': '容器项目', '/sites': '静态网站', '/domains': '域名与证书', '/domains/acme-accounts': 'ACME 账户管理', '/settings': '面板设置' }
  for (const [path, title] of Object.entries(titles)) {
    const route = await open(path)
    expect(route.path).toBe(path)
    expect(pageTitle(route)).toBe(`${title} · LPanel`)
  }
})
