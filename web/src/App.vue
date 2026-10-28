<script setup lang="ts">
import SitesView from './SitesView.vue'
import ProjectsView from './ProjectsView.vue'
import UpgradeView from './UpgradeView.vue'
import DomainsView from './DomainsView.vue'
import AccessView from './AccessView.vue'
import { computed, onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { APIError, auth, request, type User } from './api'
const user = ref<User | null>(null)
const loading = ref(true)
const submitting = ref(false)
const username = ref('')
const password = ref('')
const error = ref('')
const page = ref('overview')
const version = ref('')
const navigation = [ { id: 'overview', label: '概览', icon: '◈' }, { id: 'projects', label: '容器项目', icon: '▦' }, { id: 'sites', label: '静态网站', icon: '▤' }, { id: 'bindings', label: '域名与证书', icon: '◇' }, { id: 'settings', label: '面板设置', icon: '⚙' } ]
const title = computed(() => navigation.find(item => item.id === page.value)?.label ?? '概览')
async function refreshVersion() { const result = await request<{version:string}>('/system/version'); version.value = result.version }
onMounted(async () => {
  try { user.value = await auth.me(); await refreshVersion() } catch (e) { if (!(e instanceof APIError && e.status === 401)) error.value = e instanceof Error ? e.message : '连接服务器失败' } finally { loading.value = false }
})
async function login() {
  if (!username.value || !password.value) { error.value = '请输入管理员账号和密码'; return }
  submitting.value = true; error.value = ''
  try { user.value = await auth.login(username.value, password.value); password.value = ''; await refreshVersion() } catch (e) { error.value = e instanceof Error ? e.message : '登录失败' } finally { submitting.value = false }
}
async function logout() { try { await auth.logout(); user.value = null; page.value = 'overview' } catch (e) { ElMessage.error(e instanceof Error ? e.message : '退出失败') } }
</script>
<template>
  <div v-if="loading" class="loading-screen">正在连接面板…</div>
  <main v-else-if="!user" class="login-screen">
    <section class="login-brand"><div class="brand-mark">L</div><h1>LPanel</h1><p>让服务器管理井然有序</p><div class="brand-orbit"></div><span class="brand-caption">服务器 · 网站 · 应用</span></section>
    <section class="login-form"><div class="login-inner"><span class="eyebrow">欢迎回来</span><h2>登录管理面板</h2><p class="muted">使用管理员账号访问服务器</p><el-alert v-if="error" :title="error" type="error" :closable="false" show-icon class="login-alert"/><el-form label-position="top" @submit.prevent="login"><el-form-item label="管理员账号"><el-input v-model="username" autocomplete="username" placeholder="请输入账号" size="large" /></el-form-item><el-form-item label="密码"><el-input v-model="password" type="password" show-password autocomplete="current-password" placeholder="请输入密码" size="large" /></el-form-item><el-button native-type="submit" type="primary" size="large" :loading="submitting" class="login-submit">登录</el-button></el-form><p class="login-footnote">请妥善保管管理员账号</p></div></section>
  </main>
  <div v-else class="app-shell">
    <aside class="sidebar"><a class="brand" href="#" @click.prevent="page='overview'"><span class="brand-mark small">L</span>LPanel</a><span class="sidebar-caption">服务器工作台</span><nav><button v-for="item in navigation" :key="item.id" :class="['nav-item', { active: page === item.id }]" @click="page=item.id"><span class="nav-icon">{{ item.icon }}</span>{{ item.label }}</button></nav><div class="sidebar-bottom"><span class="status-dot"></span>管理服务已连接<span class="version">{{ version }}</span></div></aside>
    <div class="workspace"><header class="topbar"><span class="breadcrumb">工作台 <span>/</span> {{ title }}</span><div class="account"><span class="avatar">{{ user.username.slice(0,1).toUpperCase() }}</span><span>{{ user.username }}</span><el-button text @click="logout">退出</el-button></div></header><main class="page-content"><div class="page-heading"><div><span class="eyebrow">服务器管理</span><h1>{{ title }}</h1></div><span class="live-badge"><span class="status-dot"></span>已连接</span></div><el-alert v-if="error" :title="error" type="error" :closable="false" show-icon class="page-alert" /><template v-if="page==='overview'"><section class="welcome-card"><div><span class="eyebrow">欢迎使用 LPANEL</span><h2>你好，{{ user.username }}</h2><p>在这里管理你的应用、网站与域名。</p></div><span class="welcome-symbol">◈</span></section><div class="quick-grid"><button v-for="item in navigation.slice(1,4)" :key="item.id" class="quick-card" @click="page=item.id"><span class="quick-icon">{{ item.icon }}</span><strong>{{ item.label }}</strong><span>进入管理 <span aria-hidden="true">→</span></span></button></div><section class="content-card"><h3>面板信息</h3><dl class="info-list"><div><dt>当前版本</dt><dd>{{ version }}</dd></div><div><dt>当前管理员</dt><dd>{{ user.username }}</dd></div></dl></section></template><ProjectsView v-else-if="page==='projects'"/><SitesView v-else-if="page==='sites'"/><DomainsView v-else-if="page==='bindings'"/><template v-else-if="page==='settings'"><AccessView/><UpgradeView/></template><section v-else class="content-card"><el-empty :description="title" /></section></main><footer>LPanel · 服务器管理面板</footer></div>
  </div>
</template>
