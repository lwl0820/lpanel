<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { upgrades, type UpgradeState } from './upgrade-api'
import type { Operation } from './api'
const state = ref<UpgradeState | null>(null)
const repository = ref('')
const error = ref('')
const busy = ref(false)
const operation = ref<Operation | null>(null)
const reconnecting = ref(false)
let timer: ReturnType<typeof setTimeout> | undefined
let active = true
function refreshPage() { window.location.reload() }
async function load() {
  try { state.value = await upgrades.status(); repository.value = state.value.repository } catch (e) { error.value = e instanceof Error ? e.message : '读取版本失败' }
}
onMounted(async () => {
  await load()
  const id = sessionStorage.getItem('lpanel-upgrade')
  if (id) void poll(id)
})
onUnmounted(() => { active = false; if (timer) clearTimeout(timer) })
async function save() {
  busy.value = true; error.value = ''
  try { state.value = await upgrades.repository(repository.value.trim()) } catch (e) { error.value = e instanceof Error ? e.message : '保存失败' } finally { busy.value = false }
}
async function check() {
  busy.value = true; error.value = ''
  try { state.value = await upgrades.check() } catch (e) { error.value = e instanceof Error ? e.message : '检查更新失败'; await load() } finally { busy.value = false }
}
async function install() {
  busy.value = true; error.value = ''
  try { operation.value = await upgrades.install(); sessionStorage.setItem('lpanel-upgrade', operation.value.id); void poll(operation.value.id) } catch (e) { error.value = e instanceof Error ? e.message : '启动升级失败' } finally { busy.value = false }
}
async function poll(id: string) {
  if (!active) return
  try {
    operation.value = await upgrades.operation(id); reconnecting.value = false
    if (operation.value.status === 'success' || operation.value.status === 'failed') { sessionStorage.removeItem('lpanel-upgrade'); await load(); return }
  } catch { reconnecting.value = true }
  if (active) timer = setTimeout(() => { void poll(id) }, 3000)
}
</script>
<template>
  <section class="content-card upgrade-card">
    <h3>版本升级</h3>
    <el-alert v-if="error" :title="error" type="error" :closable="false" show-icon />
    <el-form label-position="top" @submit.prevent="save">
      <el-form-item label="GitHub 发布仓库"><el-input v-model="repository" placeholder="owner/repo" /><el-button :loading="busy" @click="save">保存发布源</el-button></el-form-item>
    </el-form>
    <el-descriptions v-if="state" :column="1">
      <el-descriptions-item label="当前版本">{{ state.current }}</el-descriptions-item>
      <el-descriptions-item label="最新版本">{{ state.latest || '尚未检查' }}</el-descriptions-item>
      <el-descriptions-item label="上次检查">{{ state.checked_at ? new Date(state.checked_at).toLocaleString('zh-CN') : '尚未检查' }}</el-descriptions-item>
    </el-descriptions>
    <el-alert v-if="!state?.repository" title="请先配置公开发布仓库，再检查更新" type="info" :closable="false" />
    <el-alert v-else-if="state?.error && !error" :title="state.error" type="warning" :closable="false" />
    <div class="upgrade-actions"><el-button :loading="busy" :disabled="!state?.repository" @click="check">检查更新</el-button><el-button v-if="state?.available" type="primary" :loading="busy || operation?.status === 'running'" @click="install">升级到 {{ state.latest }}</el-button></div>
    <template v-if="operation">
      <el-alert v-if="operation.status === 'running'" :title="reconnecting ? '正在重新连接面板，升级结果将在连接恢复后显示' : '升级进行中，面板可能暂时断开连接'" type="info" :closable="false" />
      <el-alert v-else :title="operation.status === 'success' ? '升级完成，刷新页面以使用新版本' : operation.error" :type="operation.status === 'success' ? 'success' : 'error'" :closable="false" />
      <el-button v-if="operation.status === 'success'" @click="refreshPage">刷新面板</el-button>
    </template>
  </section>
</template>
<style scoped>
.upgrade-card{margin-top:24px}.upgrade-card h3{margin-bottom:20px}.upgrade-card .el-alert{margin-bottom:18px}.upgrade-card .el-form{margin-top:18px}.upgrade-card .el-input{max-width:420px;margin-right:12px}.upgrade-actions{display:flex;gap:10px;margin:20px 0}
</style>
