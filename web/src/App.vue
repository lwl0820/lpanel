<script setup lang="ts">
import { onMounted, ref, watchEffect } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { APIError, auth, type User } from './api'
import { LOGIN_TITLE, pageTitle } from './router'
import AppIcon from './components/AppIcon.vue'
import LoginCard from './components/LoginCard.vue'
import type { IconName } from './icons'
const route = useRoute()
const router = useRouter()
const user = ref<User | null>(null)
const loading = ref(true)
const submitting = ref(false)
const error = ref('')
const navigation: { path: string; label: string; icon: IconName }[] = [ { path: '/', label: '概览', icon: 'overview' }, { path: '/projects', label: '容器项目', icon: 'projects' }, { path: '/sites', label: '静态网站', icon: 'sites' }, { path: '/domains', label: '域名与证书', icon: 'domains' }, { path: '/settings', label: '面板设置', icon: 'settings' } ]
watchEffect(() => { document.title = user.value ? pageTitle(route) : LOGIN_TITLE })
onMounted(async () => {
  try { user.value = await auth.me() } catch (e) { if (!(e instanceof APIError && e.status === 401)) error.value = e instanceof Error ? e.message : '连接服务器失败' } finally { loading.value = false }
})
async function login(username: string, password: string) {
  if (!username || !password) { error.value = '请输入管理员账号和密码'; return }
  submitting.value = true; error.value = ''
  try { user.value = await auth.login(username, password) } catch (e) { error.value = e instanceof Error ? e.message : '登录失败' } finally { submitting.value = false }
}
async function logout() { try { await auth.logout(); user.value = null; await router.replace('/') } catch (e) { ElMessage.error(e instanceof Error ? e.message : '退出失败') } }
</script>
<template>
  <div v-if="loading" class="loading-screen" role="status"><span class="spin"></span>正在连接面板…</div>
  <LoginCard v-else-if="!user" :error="error" :submitting="submitting" @submit="login"/>
  <div v-else class="app">
    <aside class="side">
      <RouterLink class="brand" to="/"><AppIcon name="logo"/>LPanel</RouterLink>
      <nav class="nav" aria-label="主导航"><RouterLink v-for="item in navigation" :key="item.path" :to="item.path" active-class="" exact-active-class="active"><AppIcon :name="item.icon"/><span>{{ item.label }}</span></RouterLink></nav>
      <div class="side-foot"><span class="avatar">{{ user.username.slice(0, 1).toUpperCase() }}</span><span class="who">{{ user.username }}<span>管理员</span></span><button type="button" class="icon-btn" aria-label="退出登录" title="退出登录" @click="logout"><AppIcon name="logout"/></button></div>
    </aside>
    <div class="main-col">
      <header class="topbar"><RouterLink class="brand" to="/"><AppIcon name="logo"/>LPanel</RouterLink><el-button text size="small" @click="logout">退出</el-button></header>
      <main class="main"><div class="content"><RouterView/></div></main>
    </div>
  </div>
</template>
