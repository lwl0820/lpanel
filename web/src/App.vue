<script setup lang="ts">
import { onMounted, ref, watchEffect } from 'vue'
import { useRoute } from 'vue-router'
import { ElMessage } from 'element-plus'
import { APIError, auth, type User } from './api'
import { LOGIN_TITLE, pageTitle } from './router'
import AppIcon from './components/AppIcon.vue'
import LoginCard from './components/LoginCard.vue'
import type { IconName } from './icons'
const props = defineProps<{ loginPath: string }>()
const route = useRoute()
const user = ref<User | null>(null)
const loading = ref(true)
const submitting = ref(false)
const error = ref('')
const navigation: { path: string; label: string; shortLabel?: string; icon: IconName }[] = [
  { path: '/', label: '概览', icon: 'overview' },
  { path: '/projects', label: '容器项目', shortLabel: '项目', icon: 'projects' },
  { path: '/images', label: '镜像管理', shortLabel: '镜像', icon: 'images' },
  { path: '/sites', label: '静态网站', shortLabel: '网站', icon: 'sites' },
  { path: '/domains', label: '域名与证书', shortLabel: '域名', icon: 'domains' },
  { path: '/server', label: '服务器管理', shortLabel: '服务器', icon: 'server' },
  { path: '/settings', label: '面板设置', shortLabel: '设置', icon: 'settings' },
]
watchEffect(() => { document.title = user.value ? pageTitle(route) : LOGIN_TITLE })
onMounted(async () => {
  try {
    const currentUser = await auth.me()
    if (props.loginPath) {
      window.location.replace('/')
      return
    }
    user.value = currentUser
  } catch (e) {
    const guest = e instanceof APIError && (e.status === 401 || (props.loginPath && e.status === 404))
    if (!guest) error.value = e instanceof Error ? e.message : '连接服务器失败'
  }
  loading.value = false
})
async function login(username: string, password: string) {
  if (!username || !password) { error.value = '请输入管理员账号和密码'; return }
  submitting.value = true; error.value = ''
  try {
    const currentUser = await auth.login(username, password, props.loginPath)
    if (props.loginPath) {
      window.location.replace('/')
      return
    }
    user.value = currentUser
  } catch (e) { error.value = e instanceof Error ? e.message : '登录失败' } finally { submitting.value = false }
}
async function logout() {
  try { await auth.logout() } catch (e) {
    if (!(e instanceof APIError && (e.status === 401 || e.status === 404))) {
      ElMessage.error(e instanceof Error ? e.message : '退出失败')
      return
    }
  }
  window.location.replace('/')
}
</script>
<template>
  <div v-if="loading" class="loading-screen" role="status"><span class="spin"></span>正在连接面板…</div>
  <LoginCard v-else-if="!user" :error="error" :submitting="submitting" @submit="login"/>
  <div v-else class="app">
    <aside class="side">
      <RouterLink class="brand" to="/"><AppIcon name="logo"/>LPanel</RouterLink>
      <nav class="nav" aria-label="主导航">
        <RouterLink v-for="item in navigation" :key="item.path" :to="item.path" :aria-label="item.label" :class="{ active: route.path.startsWith(`${item.path}/`) }" active-class="" exact-active-class="active">
          <AppIcon :name="item.icon"/><span :class="{ 'nav-full': item.shortLabel }">{{ item.label }}</span><span v-if="item.shortLabel" class="nav-short">{{ item.shortLabel }}</span>
        </RouterLink>
      </nav>
      <div class="side-foot"><span class="avatar">{{ user.username.slice(0, 1).toUpperCase() }}</span><span class="who">{{ user.username }}<span>管理员</span></span><button type="button" class="icon-btn" aria-label="退出登录" title="退出登录" @click="logout"><AppIcon name="logout"/></button></div>
    </aside>
    <div class="main-col">
      <header class="topbar"><RouterLink class="brand" to="/"><AppIcon name="logo"/>LPanel</RouterLink><el-button text size="small" @click="logout">退出</el-button></header>
      <main class="main">
        <div class="content">
          <nav v-if="route.path.startsWith('/server/')" class="server-nav" aria-label="服务器管理">
            <RouterLink to="/server/monitor">服务器监控</RouterLink>
            <RouterLink to="/server/processes">实时进程</RouterLink>
            <RouterLink to="/server/files">文件管理</RouterLink>
            <RouterLink to="/server/firewall">防火墙</RouterLink>
            <RouterLink to="/server/swap">交换空间</RouterLink>
            <RouterLink to="/server/time">时区与时间同步</RouterLink>
            <RouterLink to="/server/dns">DNS 设置</RouterLink>
          </nav>
          <RouterView/>
        </div>
      </main>
    </div>
  </div>
</template>
