<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { upgrades, type UpgradeState } from '../upgrade-api'
import type { Operation } from '../api'
import { errorText, formatTime } from '../format'
import AppIcon from './AppIcon.vue'
// 升级期间面板会重启，请求失败表示正在重连而不是出错，所以不用 waitOperation：
// 每 3 秒轮询，操作 id 存在 sessionStorage，刷新页面后继续跟踪
const state = ref<UpgradeState | null>(null)
const repository = ref('')
const loading = ref(true)
const error = ref('')
const pending = ref<'' | 'save' | 'auto-update' | 'check' | 'install'>('')
const operation = ref<Operation | null>(null)
const reconnecting = ref(false)
let timer: ReturnType<typeof setTimeout> | undefined
let active = true
const running = () => operation.value?.status === 'queued' || operation.value?.status === 'running'
function refreshPage() { window.location.reload() }
async function load() {
  try { state.value = await upgrades.status(); repository.value = state.value.repository; error.value = '' } catch (e) { error.value = errorText(e, '读取版本失败') } finally { loading.value = false }
}
onMounted(async () => {
  await load()
  const id = sessionStorage.getItem('lpanel-upgrade')
  if (id) void poll(id)
})
onUnmounted(() => { active = false; if (timer) clearTimeout(timer) })
async function save() {
  if (pending.value) return
  pending.value = 'save'; error.value = ''
  try { state.value = await upgrades.repository(repository.value.trim()); repository.value = state.value.repository; ElMessage.success('发布仓库已保存') } catch (e) { error.value = errorText(e, '保存失败') } finally { pending.value = '' }
}
async function setAutoUpdate(value: string | number | boolean) {
  pending.value = 'auto-update'
  error.value = ''
  try {
    state.value = await upgrades.setAutoUpdate(value === true)
    ElMessage.success(state.value.auto_update ? '已开启自动更新' : '已关闭自动更新')
  } catch (e) {
    error.value = errorText(e, '保存自动更新设置失败')
  } finally {
    pending.value = ''
  }
}
async function check() {
  pending.value = 'check'; error.value = ''
  try { state.value = await upgrades.check(); if (state.value.available) ElMessage.success(`发现新版本 ${state.value.latest}`); else if (!state.value.error) ElMessage.success('已是最新版本') } catch (e) { error.value = errorText(e, '检查更新失败'); await load() } finally { pending.value = '' }
}
async function install() {
  if (!state.value) return
  try { await ElMessageBox.confirm('升级期间面板会重启并短暂断开连接，已部署的容器和网站不受影响。', `升级到 ${state.value.latest}`, { type: 'warning', confirmButtonText: '开始升级', cancelButtonText: '取消' }) } catch { return }
  pending.value = 'install'; error.value = ''
  try { operation.value = await upgrades.install(); sessionStorage.setItem('lpanel-upgrade', operation.value.id); void poll(operation.value.id) } catch (e) { error.value = errorText(e, '启动升级失败') } finally { pending.value = '' }
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
  <section class="panel">
    <div class="panel-h"><h2>面板升级</h2><span v-if="state?.available" class="pill run">有新版本</span></div>
    <div v-if="loading" class="state" role="status"><span class="spin"></span><span>正在加载…</span></div>
    <template v-else>
      <div class="panel-b form">
        <div v-if="operation && running()" class="banner run" role="status"><span class="spin"></span><span class="txt">{{ reconnecting ? '正在重新连接面板，升级结果将在连接恢复后显示…' : operation.status === 'queued' ? '升级已提交，排队中…' : '正在升级，面板可能暂时断开连接…' }}</span></div>
        <div v-else-if="operation?.status === 'success'" class="banner ok" role="status"><AppIcon name="check"/><span class="txt">升级完成，刷新页面以使用新版本。</span><el-button size="small" @click="refreshPage">刷新面板</el-button></div>
        <div v-else-if="operation?.status === 'failed'" class="banner bad" role="alert"><AppIcon name="alert"/><span class="txt"><b>升级失败</b>{{ operation.error }}</span></div>
        <el-alert v-if="error" :title="error" type="error" :closable="false" show-icon/>
        <dl v-if="state" class="kv">
          <dt>当前版本</dt><dd>{{ state.current }}</dd>
          <dt>最新版本</dt><dd><template v-if="state.latest">{{ state.latest }}</template><span v-else class="muted">尚未检查</span></dd>
          <dt>上次检查</dt><dd><template v-if="state.checked_at">{{ formatTime(state.checked_at) }}</template><span v-else class="muted">—</span></dd>
        </dl>
        <div class="field">
          <label for="repo">发布仓库</label>
          <div class="inline"><el-input id="repo" v-model="repository" class="mono-input" placeholder="owner/repo" spellcheck="false" @keydown.enter.prevent="save"/><el-button :loading="pending === 'save'" :disabled="!!pending" @click="save">保存</el-button></div>
          <span class="hint">从该 GitHub 仓库的 Releases 检查新版本。</span>
        </div>
        <div v-if="state" class="field">
          <label for="panel-auto-update">自动更新</label>
          <el-switch id="panel-auto-update" :model-value="state.auto_update"
            :loading="pending === 'auto-update'" :disabled="!!pending" @change="setAutoUpdate"/>
          <span class="hint">开启后，面板会在后台检查时自动安装可用的新正式版。</span>
        </div>
        <el-alert v-if="state && !state.repository" title="请先配置公开发布仓库，再检查更新。" type="info" :closable="false"/>
        <el-alert v-else-if="state?.error && !error" :title="state.error" type="warning" :closable="false"/>
      </div>
      <div class="panel-f">
        <el-button :loading="pending === 'check'" :disabled="!state?.repository || !!pending || running()" @click="check"><AppIcon v-if="pending !== 'check'" name="refresh"/>检查更新</el-button>
        <el-button v-if="state?.available" type="primary" :loading="pending === 'install' || running()" :disabled="!!pending" @click="install">升级到 {{ state.latest }}</el-button>
      </div>
    </template>
  </section>
</template>
