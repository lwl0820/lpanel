<script setup lang="ts">
import { computed, nextTick, onMounted, reactive, ref, useTemplateRef, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { domains } from '../domain-api'
import { errorText, formatBytes } from '../format'
import { programs, type Program, type ProgramAction, type ProgramPort, type ProgramRuntime, type ProgramSettings } from '../programs-api'
import { useOperation } from '../useOperation'
import AppIcon from '../components/AppIcon.vue'
import BindDomainDialog, { type BindPreset, type BindResult } from '../components/BindDomainDialog.vue'
import ListState from '../components/ListState.vue'
import OperationBanner from '../components/OperationBanner.vue'
import PageHeader from '../components/PageHeader.vue'
import RowMenu, { type MenuItem } from '../components/RowMenu.vue'

const statuses = {
  running: { label: '运行中', color: 'ok' },
  stopped: { label: '已停止', color: 'idle' },
  starting: { label: '启动中', color: 'run' },
  stopping: { label: '停止中', color: 'run' },
  failed: { label: '启动失败', color: 'bad' },
  unknown: { label: '状态未知', color: 'warn' },
} satisfies Record<Program['status'], { label: string; color: string }>
const actionLabels = { start: '启动', stop: '停止', restart: '重启' }

const entries = ref<Program[]>([])
const selectedId = ref('')
const current = computed(() => entries.value.find(item => item.id === selectedId.value))
const loading = ref(true)
const listError = ref('')
const dependencyError = ref('')
const detailError = ref('')
const refresh = ref(0)
const runtime = ref<ProgramRuntime>()
const runtimeError = ref('')
const tab = ref('logs')
const settings = reactive<ProgramSettings>({ name: '', entry: '', args: '', run_user: '' })
const saving = ref(false)
const settingsError = ref('')
const pendingAction = ref<ProgramAction>()
const actionError = ref('')

const dialog = ref<'create' | 'copy' | 'delete'>()
const dialogTarget = ref<Program>()
const dialogName = ref('')
const dialogError = ref('')
const submitting = ref(false)
const removeFiles = ref(false)

const bindOpen = ref(false)
const bindPreset = ref<BindPreset>()
const { operation, label: operationLabel, busy: certificateBusy, track } = useOperation(domains.operation)

const menu = computed<MenuItem[]>(() => [
  { label: '启动设置', icon: 'settings', onSelect: () => { tab.value = 'settings' } },
  { label: '重启', icon: 'restart', disabled: !canRestart.value, onSelect: () => runAction('restart') },
  { label: '复制程序', icon: 'projects', onSelect: () => openDialog('copy') },
  { label: '删除程序', icon: 'trash', danger: true, divided: true, onSelect: () => openDialog('delete') },
])
const status = computed(() => current.value?.status ?? 'unknown')
const transitioning = computed(() => status.value === 'starting' || status.value === 'stopping')
const primaryAction = computed<ProgramAction>(() => status.value === 'running' || status.value === 'starting' ? 'stop' : 'start')
const canRestart = computed(() => !!current.value?.entry && !dependencyError.value && !transitioning.value)
const metrics = computed(() => {
  const sample = status.value === 'running' ? runtime.value : undefined
  return [
    { label: 'PID', value: sample?.pid || (status.value === 'running' && current.value?.pid) || '—' },
    { label: 'CPU', value: sample?.cpu_percent == null ? '—' : `${sample.cpu_percent.toFixed(1)}%` },
    { label: '内存', value: sample?.memory_bytes == null ? '—' : formatBytes(sample.memory_bytes) },
    { label: 'TCP 连接', value: sample?.connections ?? '—' },
  ]
})

function selectProgram(id: string) {
  if (id === selectedId.value) return
  selectedId.value = id
  tab.value = 'logs'
}

async function load() {
  loading.value = true
  listError.value = ''
  try {
    const result = await programs.list()
    entries.value = result.programs
    dependencyError.value = result.dependency_error
    if (!current.value) selectProgram(entries.value[0]?.id ?? '')
  } catch (e) {
    listError.value = errorText(e, '读取程序列表失败')
  } finally {
    loading.value = false
  }
}
onMounted(load)

watch(selectedId, () => {
  runtime.value = undefined
  runtimeError.value = ''
  detailError.value = ''
  settingsError.value = ''
  actionError.value = ''
  if (current.value) {
    const { name, entry, args, run_user } = current.value
    Object.assign(settings, { name, entry, args, run_user })
  }
})

watch([selectedId, refresh], ([id], _, onCleanup) => {
  if (!id) return
  let active = true
  let timer: ReturnType<typeof setTimeout>
  async function poll() {
    const results = await Promise.allSettled([programs.list(), programs.runtime(id)])
    if (!active) return
    const [list, sample] = results
    if (list.status === 'fulfilled') {
      entries.value = list.value.programs
      dependencyError.value = list.value.dependency_error
      detailError.value = ''
      if (!current.value) {
        selectProgram(entries.value[0]?.id ?? '')
        return
      }
    } else {
      detailError.value = errorText(list.reason, '刷新程序状态失败')
    }
    runtime.value = sample.status === 'fulfilled' ? sample.value : undefined
    runtimeError.value = sample.status === 'fulfilled' ? sample.value.message : errorText(sample.reason, '读取运行信息失败')
    timer = setTimeout(poll, 3000)
  }
  onCleanup(() => {
    active = false
    clearTimeout(timer)
  })
  void poll()
})

const logText = ref('')
const logStatus = ref('正在连接…')
const logError = ref('')
const logsNode = useTemplateRef('logsNode')
watch([selectedId, tab], ([id, page], _, onCleanup) => {
  if (!id || page !== 'logs') return
  logText.value = ''
  logStatus.value = '正在连接…'
  logError.value = ''
  const source = new window.EventSource(programs.logsURL(id))
  const receive = (event: { data: string; type: string }) => {
    const node = logsNode.value
    const atEnd = !node || node.scrollHeight - node.scrollTop - node.clientHeight <= 8
    const text = JSON.parse(event.data) as string
    const combined = event.type === 'snapshot' ? text : logText.value + text
    logText.value = combined.slice(-256 * 1024)
    logStatus.value = '跟踪中'
    logError.value = ''
    if (atEnd || event.type === 'snapshot') {
      void nextTick(() => { if (logsNode.value) logsNode.value.scrollTop = logsNode.value.scrollHeight })
    }
  }
  source.addEventListener('snapshot', receive)
  source.addEventListener('append', receive)
  source.addEventListener('log-error', (event: { data: string; type: string }) => {
    logError.value = JSON.parse(event.data) as string
    logStatus.value = '读取失败'
    source.close()
  })
  source.onerror = () => {
    const closed = source.readyState === window.EventSource.CLOSED
    logStatus.value = closed ? '连接已关闭' : '正在重新连接…'
    if (closed && !logError.value) logError.value = '无法连接日志，请切换标签后重试'
  }
  onCleanup(() => source.close())
})

function apply(updated: Program) {
  const index = entries.value.findIndex(item => item.id === updated.id)
  if (index === -1) entries.value.push(updated)
  else entries.value[index] = updated
  refresh.value += 1
}

async function runAction(action: ProgramAction) {
  if (!current.value || pendingAction.value) return
  const id = current.value.id
  pendingAction.value = action
  actionError.value = ''
  try {
    apply(await programs.action(id, action))
  } catch (e) {
    if (selectedId.value === id) actionError.value = errorText(e, `${actionLabels[action]}失败`)
    refresh.value += 1
  } finally {
    pendingAction.value = undefined
  }
}

async function saveSettings() {
  if (!current.value || saving.value) return
  const id = current.value.id
  if (!settings.name.trim() || !settings.run_user.trim()) {
    settingsError.value = '请填写程序名称和运行用户'
    return
  }
  saving.value = true
  settingsError.value = ''
  try {
    const updated = await programs.save(id, { ...settings })
    apply(updated)
    if (selectedId.value === id) {
      const { name, entry, args, run_user } = updated
      Object.assign(settings, { name, entry, args, run_user })
    }
    ElMessage.success('启动设置已保存')
  } catch (e) {
    if (selectedId.value === id) settingsError.value = errorText(e, '保存启动设置失败')
  } finally {
    saving.value = false
  }
}

function openDialog(kind: 'create' | 'copy' | 'delete') {
  dialogTarget.value = current.value
  dialogName.value = kind === 'copy' && current.value ? `${current.value.name} 副本` : ''
  dialogError.value = ''
  removeFiles.value = false
  dialog.value = kind
}

const dialogOpen = computed({
  get: () => !!dialog.value,
  set: (open: boolean) => { if (!open) dialog.value = undefined },
})
const dialogTitle = computed(() => dialog.value === 'delete' ? '删除程序' : dialog.value === 'copy' ? '复制程序' : '新建程序')

async function submitDialog() {
  const kind = dialog.value
  const target = dialogTarget.value
  const name = dialogName.value.trim()
  if (submitting.value || !kind) return
  if (kind !== 'delete' && !name) {
    dialogError.value = '请填写程序名称'
    return
  }
  submitting.value = true
  dialogError.value = ''
  try {
    if (kind === 'delete' && target) {
      await programs.remove(target.id, removeFiles.value)
      entries.value = entries.value.filter(item => item.id !== target.id)
      selectProgram(entries.value[0]?.id ?? '')
      ElMessage.success('程序已删除')
    } else if (kind === 'copy' && target) {
      const result = await programs.copy(target.id, name)
      apply(result.program)
      selectProgram(result.program.id)
      if (result.skipped_links) ElMessage.warning(`程序已复制，跳过了 ${result.skipped_links} 个符号链接`)
      else ElMessage.success('程序已复制')
    } else if (kind === 'create') {
      const created = await programs.create(name)
      apply(created)
      selectProgram(created.id)
      ElMessage.success('程序已创建')
    }
    dialog.value = undefined
  } catch (e) {
    dialogError.value = errorText(e, `${dialogTitle.value}失败`)
  } finally {
    submitting.value = false
  }
}

function bindPort(item: Program, port: ProgramPort) {
  bindPreset.value = { kind: 'program', id: item.id, name: item.name, address: port.address, port: port.port }
  bindOpen.value = true
}

async function bindingDone(result: BindResult) {
  if (result.operation) await track(result.operation, `为 ${result.domain} 申请证书`, '证书已启用')
  else ElMessage.success('域名已绑定')
}
</script>

<template>
  <PageHeader title="程序管理" description="管理直接运行在服务器上的程序。">
    <el-button type="primary" :disabled="loading || !!listError" @click="openDialog('create')"><AppIcon name="plus"/>新建程序</el-button>
  </PageHeader>

  <OperationBanner :operation="operation" :label="operationLabel" @close="operation = null"/>
  <el-alert v-if="dependencyError" title="程序无法启停，请检查服务器上的 Supervisor" type="warning" :description="dependencyError" :closable="false" show-icon/>

  <ListState :loading="loading" :error="listError" :empty="!entries.length" icon="play" title="还没有程序" description="创建程序后，可上传文件并设置启动方式。" @retry="load">
    <div class="program-management">
      <div class="program-split">
        <section class="panel program-list-panel" aria-label="程序列表">
          <div class="program-list-heading"><h2>程序列表</h2><span>{{ entries.length }}</span></div>
          <ul class="program-list">
            <li v-for="item in entries" :key="item.id">
              <button type="button" class="program-choice" :aria-pressed="selectedId === item.id" @click="selectProgram(item.id)">
                <strong>{{ item.name }}</strong>
                <span class="pill program-status" :class="statuses[item.status].color">{{ statuses[item.status].label }}</span>
                <small v-if="item.entry" class="mono">{{ item.entry }}</small>
              </button>
            </li>
          </ul>
        </section>

        <section v-if="current" class="panel program-detail" aria-label="程序详情">
          <div class="program-line">
            <div class="program-header">
              <div class="program-identity">
                <span class="program-mark"><AppIcon name="play"/></span>
                <h2>{{ current.name }}</h2>
                <span class="pill program-status" :class="statuses[current.status].color">{{ statuses[current.status].label }}</span>
              </div>
              <small class="mono muted">{{ current.entry || '未设置主程序' }}</small>
            </div>
            <div class="program-actions">
              <el-button :type="primaryAction === 'start' ? 'primary' : 'default'" :loading="pendingAction === primaryAction" :disabled="!!pendingAction || !!dependencyError || transitioning || (primaryAction === 'start' && !current.entry)" @click="runAction(primaryAction)">
                <AppIcon :name="primaryAction === 'start' ? 'play' : 'stop'"/>{{ actionLabels[primaryAction] }}
              </el-button>
              <RowMenu :label="current.name" :items="menu" trigger-text="更多" :disabled="!!pendingAction"/>
            </div>
          </div>
          <p v-if="detailError || actionError || (!dependencyError && current.status_message)" class="program-feedback" :class="{ 'program-danger': detailError || actionError || status === 'failed' }" :role="detailError || actionError || status === 'failed' ? 'alert' : 'status'">{{ detailError || actionError || (!dependencyError && current.status_message) }}</p>

          <dl class="program-metrics" aria-label="主进程运行信息">
            <div v-for="metric in metrics" :key="metric.label">
              <dt>{{ metric.label }}</dt>
              <dd>{{ metric.value }}</dd>
            </div>
          </dl>
          <p class="program-metric-note">仅统计主进程</p>

          <div class="program-line program-tools">
            <div class="program-ports">
              <span>监听端口</span>
              <template v-if="status === 'running' && runtime?.ports.length">
                <button v-for="port in runtime.ports" :key="`${port.address}:${port.port}`" type="button" class="program-port mono" :aria-label="`为 ${port.address} 的 ${port.port} 端口绑定域名`" :title="`为 ${port.address} 的 ${port.port} 端口绑定域名`" :disabled="certificateBusy" @click="bindPort(current, port)">
                  {{ runtime.ports.filter(item => item.port === port.port).length > 1 ? `${port.address.includes(':') ? `[${port.address}]` : port.address}:${port.port}` : `:${port.port}` }}
                </button>
              </template>
              <span v-else>—</span>
            </div>
            <RouterLink class="program-directory" :to="{ path: '/server/files', query: { path: current.directory } }"><AppIcon name="folder"/>打开目录</RouterLink>
          </div>
          <p v-if="runtimeError && runtimeError !== current.status_message && runtimeError !== dependencyError" class="program-feedback" role="status">{{ runtimeError }}</p>

          <el-tabs v-model="tab" :key="current.id" class="program-tabs" aria-label="程序详情标签">
            <el-tab-pane name="logs" label="运行日志">
              <div v-if="tab === 'logs'">
                <div class="program-log-heading"><span>最近输出</span><span role="status">{{ logStatus }}</span></div>
                <p v-if="logError" role="alert" class="program-feedback program-danger">{{ logError }}</p>
                <pre ref="logsNode" class="program-log" tabindex="0" aria-label="运行日志">{{ logText || (logStatus === '跟踪中' ? '暂无日志' : '') }}</pre>
              </div>
            </el-tab-pane>
            <el-tab-pane name="settings" label="启动设置">
              <el-form :model="settings" label-position="top" class="program-settings" @submit.prevent="saveSettings">
                <el-form-item label="程序名称" required><el-input v-model="settings.name" :disabled="saving"/></el-form-item>
                <el-form-item label="主程序（相对路径）">
                  <el-input v-model="settings.entry" class="mono-input" placeholder="例如 bin/server 或 start.sh" :disabled="saving"/>
                  <p class="program-field-help">保存时会为所选文件添加执行权限。脚本需在首行指定解释器。</p>
                </el-form-item>
                <el-form-item label="启动参数">
                  <el-input v-model="settings.args" class="mono-input" placeholder="例如 --port 8080" :disabled="saving"/>
                  <p class="program-field-help">含空格的单个参数请用引号括起来。</p>
                </el-form-item>
                <el-form-item label="运行用户" required>
                  <el-input v-model="settings.run_user" :disabled="saving"/>
                  <p class="program-field-help">使用服务器上已有的非特权用户。更换用户后，请核对已有文件和子目录的读写权限，确保新用户可以进入程序所需目录。</p>
                </el-form-item>
                <p v-if="status === 'running'" class="program-field-help">保存后，重启程序即可应用新设置。</p>
                <p v-if="settingsError" role="alert" class="program-feedback program-danger">{{ settingsError }}</p>
                <el-button type="primary" native-type="submit" :loading="saving">保存设置</el-button>
              </el-form>
            </el-tab-pane>
          </el-tabs>
        </section>
      </div>
    </div>
  </ListState>

  <el-dialog v-model="dialogOpen" :title="dialogTitle" width="min(520px, calc(100vw - 32px))" append-to-body :show-close="!submitting" :close-on-click-modal="!submitting" :close-on-press-escape="!submitting">
    <template v-if="dialog === 'delete'">
      <p>删除「{{ dialogTarget?.name }}」会停止程序，并移除托管设置及该程序的域名绑定。</p>
      <p class="program-field-help">默认保留工作目录及其中的文件。</p>
      <el-checkbox v-model="removeFiles" :disabled="submitting">同时删除工作目录及其中的文件</el-checkbox>
      <p class="program-dialog-path mono">{{ dialogTarget?.directory }}</p>
    </template>
    <el-form v-else label-position="top" @submit.prevent="submitDialog">
      <el-form-item label="程序名称" required><el-input v-model="dialogName" :disabled="submitting" autofocus/></el-form-item>
      <p v-if="dialog === 'copy'" class="program-field-help">复制工作目录和启动设置。副本初始为停止状态，域名仍归原程序。</p>
    </el-form>
    <p v-if="dialogError" role="alert" class="program-feedback program-danger">{{ dialogError }}</p>
    <template #footer>
      <el-button :disabled="submitting" @click="dialogOpen = false">取消</el-button>
      <el-button :type="dialog === 'delete' ? 'danger' : 'primary'" :loading="submitting" @click="submitDialog">{{ dialog === 'delete' ? '删除' : dialog === 'copy' ? '复制' : '创建' }}</el-button>
    </template>
  </el-dialog>

  <BindDomainDialog v-model:open="bindOpen" :preset="bindPreset" @done="bindingDone"/>
</template>
