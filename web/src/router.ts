import { createRouter, createWebHistory, type RouteLocationNormalizedLoaded, type RouteRecordRaw, type RouterHistory } from 'vue-router'
import LoginCard from './components/LoginCard.vue'
export const routes: RouteRecordRaw[] = [
  { path: '/', component: () => import('./views/OverviewView.vue'), meta: { title: '概览' } },
  { path: '/projects', component: () => import('./views/ProjectsView.vue'), meta: { title: '容器项目' } },
  { path: '/images', component: () => import('./views/ImagesView.vue'), meta: { title: '镜像管理' } },
  { path: '/sites', component: () => import('./views/SitesView.vue'), meta: { title: '静态网站' } },
  { path: '/domains', component: () => import('./views/DomainsView.vue'), meta: { title: '域名与证书' } },
  { path: '/domains/acme-accounts', component: () => import('./views/AcmeAccountsView.vue'), meta: { title: 'ACME 账户管理' } },
  { path: '/server', redirect: '/server/files' },
  { path: '/server/monitor', component: () => import('./views/ServerMonitorView.vue'), meta: { title: '服务器监控' } },
  { path: '/server/processes', component: () => import('./views/ServerProcessesView.vue'), meta: { title: '实时进程' } },
  { path: '/server/files', component: () => import('./views/ServerFilesView.vue'), meta: { title: '文件管理' } },
  { path: '/server/firewall', component: () => import('./views/ServerFirewallView.vue'), meta: { title: '防火墙' } },
  { path: '/server/swap', component: () => import('./views/ServerSwapView.vue'), meta: { title: '交换空间' } },
  { path: '/server/time', component: () => import('./views/ServerTimeView.vue'), meta: { title: '时区与时间同步' } },
  { path: '/server/dns', component: () => import('./views/ServerDNSView.vue'), meta: { title: 'DNS 设置' } },
  { path: '/settings', component: () => import('./views/SettingsView.vue'), meta: { title: '面板设置' } },
  { path: '/:pathMatch(.*)*', redirect: '/' },
]
export const routeLabel = (route: RouteLocationNormalizedLoaded) => String(route.meta.title ?? '概览')
// document.title 由 App 按登录状态设置：未登录为 LOGIN_TITLE，登录后为 pageTitle(当前路由)
export const pageTitle = (route: RouteLocationNormalizedLoaded) => `${routeLabel(route)} · LPanel`
export const LOGIN_TITLE = '登录 · LPanel'
export const createAppRouter = (history: RouterHistory = createWebHistory(), loginPath = '') => createRouter({
  history,
  routes: loginPath ? [{ path: loginPath, component: LoginCard, strict: true, sensitive: true }] : routes,
})
