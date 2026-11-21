<script setup lang="ts">
import { computed, onMounted, reactive, ref, shallowReactive } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage, type ButtonInstance, type FormInstance, type FormRules } from 'element-plus'
import { projects, type Project, type ProjectContainer, type ProjectPort } from '../projects-api'
import { domains, type Binding } from '../domain-api'
import { useOperation } from '../useOperation'
import { errorText, formatTime } from '../format'
import AppIcon from '../components/AppIcon.vue'
import PageHeader from '../components/PageHeader.vue'
import ListState from '../components/ListState.vue'
import RowMenu, { type MenuItem } from '../components/RowMenu.vue'
import OperationBanner from '../components/OperationBanner.vue'
import BindDomainDialog, { type BindPreset, type BindResult } from '../components/BindDomainDialog.vue'
const items = ref<Project[]>([])
const bindings = ref<Binding[]>([])
const loading = ref(true)
const loadError = ref('')
const { operation, label: operationLabel, busy, track } = useOperation(projects.operation)
type ProjectRuntime = { state: 'loading' } | { state: 'failed'; message: string } | { state: 'ready'; containers: ProjectContainer[] }
const runtimes = shallowReactive(new Map<string, ProjectRuntime>())
const expanded = reactive(new Set<string>())

async function refreshStatus(id: string) {
  const pending = { state: 'loading' } as const
  runtimes.set(id, pending)
  try {
    const containers = await projects.status(id)
    if (runtimes.get(id) === pending) runtimes.set(id, { state: 'ready', containers })
  } catch (e) {
    if (runtimes.get(id) === pending) runtimes.set(id, { state: 'failed', message: errorText(e, '读取容器状态失败') })
  }
}

const containerStates: Record<string, { label: string; tone: string }> = {
  running: { label: '运行中', tone: 'ok' },
  restarting: { label: '正在重启', tone: 'warn' },
  exited: { label: '已停止', tone: 'idle' },
  created: { label: '未启动', tone: 'idle' },
  paused: { label: '已暂停', tone: 'warn' },
  removing: { label: '正在移除', tone: 'run' },
  dead: { label: '异常', tone: 'bad' },
}
const healthLabels: Record<string, string> = { healthy: '正常', unhealthy: '异常', starting: '检查中' }

function runtimeSummary(runtime?: ProjectRuntime) {
  if (!runtime || runtime.state === 'loading') return { label: '读取中', tone: 'run' }
  if (runtime.state === 'failed') return { label: '读取失败', tone: 'bad' }
  const { containers } = runtime
  if (!containers.length) return { label: '暂无容器', tone: 'idle' }
  if (containers.some(c => c.Health === 'unhealthy' || c.State === 'dead')) return { label: '状态异常', tone: 'bad' }
  if (containers.some(c => c.State === 'restarting')) return containerStates.restarting
  const running = containers.filter(c => c.State === 'running').length
  if (running === containers.length) return containerStates.running
  if (running) return { label: '部分运行', tone: 'warn' }
  return { label: '未运行', tone: 'idle' }
}

const rows = computed(() => items.value.map(item => {
  const runtime = runtimes.get(item.id)
  const containers = runtime?.state === 'ready' ? runtime.containers : []
  const hasActiveContainer = containers.some(c => c.State === 'running' || c.State === 'restarting' || c.State === 'paused')
  return { item, runtime, containers, hasActiveContainer, summary: runtimeSummary(runtime) }
}))

async function load() {
  loadError.value = ''
  try {
    [items.value, bindings.value] = await Promise.all([projects.list(), domains.list()])
    for (const item of items.value) void refreshStatus(item.id)
  } catch (e) {
    loadError.value = errorText(e, '读取项目失败')
  } finally {
    loading.value = false
  }
}
// 概览“开始使用”跳转时带 ?new=1，直接打开新建
const route = useRoute()
const router = useRouter()
onMounted(() => { void load(); if (route.query.new) { void edit(); void router.replace('/projects') } })
async function reload() { loading.value = true; await load() }
const domainsOf = (id: string) => bindings.value.filter(b => b.kind === 'project' && b.resource_id === id)
const verbs = { start: ['启动', '已启动'], stop: ['停止', '已停止'], restart: ['重启', '已重启'], delete: ['删除', '已删除'] }
async function action(item: Project, name: keyof typeof verbs, removeFiles = false) {
  const runtime = runtimes.get(item.id)
  const command = name === 'start' && runtime?.state === 'ready' && !runtime.containers.length ? 'deploy' : name
  busy.value = true
  try {
    const result = await track(await projects.action(item.id, command, removeFiles), `${verbs[name][0]} ${item.name}`, `${item.name} ${verbs[name][1]}`)
    if (name === 'delete' && result?.status === 'success') {
      runtimes.delete(item.id)
      expanded.delete(item.id)
      await load()
    } else if (result) {
      await refreshStatus(item.id)
    }
  } catch (e) {
    busy.value = false
    ElMessage.error(errorText(e, '操作失败'))
  }
}
const menu = (item: Project): MenuItem[] => [
  { label: '绑定域名', icon: 'link', onSelect: () => bind(item) },
  { label: '项目日志', icon: 'log', onSelect: () => openLogs(item) },
  { label: '删除项目', icon: 'trash', danger: true, divided: true, onSelect: () => {
    deleteTarget.value = item
    deleteFiles.value = false
    deleteOpen.value = true
  } },
]

const deleteTarget = ref<Project>()
const deleteFiles = ref(false)
const deleteOpen = ref(false)
const cancelDelete = ref<ButtonInstance>()

function confirmDelete() {
  const item = deleteTarget.value
  if (!item || busy.value) return
  deleteOpen.value = false
  void action(item, 'delete', deleteFiles.value)
}
// 编辑抽屉
const editor = ref(false)
const editorLoading = ref(false)
const editorError = ref('')
const selected = ref<Project | null>(null)
const form = reactive({ name: '', compose: '', environment: '' })
const formRef = ref<FormInstance>()
const rules: FormRules = { name: [{ required: true, whitespace: true, message: '请填写项目名称', trigger: ['blur', 'change'] }], compose: [{ required: true, whitespace: true, message: '请填写 Compose 配置', trigger: ['blur', 'change'] }] }
const ports = ref<ProjectPort[]>([])
const scanned = ref(false)
const scanning = ref(false)
const saving = ref(false)
async function edit(item?: Project) {
  selected.value = item ?? null; editorError.value = ''; ports.value = []; scanned.value = false
  Object.assign(form, { name: '', compose: '', environment: '' }); formRef.value?.clearValidate()
  editor.value = true
  if (!item) return
  editorLoading.value = true
  try { const data = await projects.get(item.id); Object.assign(form, { name: data.name, compose: data.compose ?? '', environment: data.environment ?? '' }) } catch (e) { editorError.value = errorText(e, '读取项目失败') } finally { editorLoading.value = false }
}
async function scan() {
  scanning.value = true; editorError.value = ''
  try { ports.value = await projects.scan({ ...form, id: selected.value?.id }); scanned.value = true } catch (e) { editorError.value = errorText(e, '扫描端口失败') } finally { scanning.value = false }
}
const hostPort = (p: ProjectPort) => p.source === 'expose' ? '未发布（仅内部访问）' : p.published && p.published !== '0' ? `${p.host_ip ? p.host_ip + ':' : ''}${p.published}` : '部署后自动分配'
async function save() {
  if (!(await formRef.value?.validate().catch(() => false)) || saving.value) return
  saving.value = true; editorError.value = ''
  try {
    const result = selected.value
      ? await projects.update(selected.value.id, form)
      : await projects.create(form)
    editor.value = false
    if (result.operation) {
      void track(result.operation, `部署 ${result.name}`, `${result.name} 部署完成`).then(outcome => {
        if (outcome) void refreshStatus(result.id)
      })
    } else if (result.deployment_error) {
      ElMessage.warning(result.deployment_error)
    }
    await load()
  } catch (e) {
    editorError.value = errorText(e, '保存失败')
  } finally {
    saving.value = false
  }
}
// 日志抽屉独立于配置编辑，可查看项目或单个容器
const logOpen = ref(false)
const logTarget = ref<{ project: Project; container?: ProjectContainer }>()
const logLoading = ref(false)
const logError = ref('')
const log = ref('')
const logTitle = computed(() => {
  const target = logTarget.value
  if (!target) return '日志'
  return target.container ? `${target.container.Name} · 容器日志` : `${target.project.name} · 项目日志`
})

async function refreshLogs() {
  const target = logTarget.value
  if (!target || logLoading.value) return
  logLoading.value = true
  logError.value = ''
  try {
    const result = target.container
      ? await projects.containerLogs(target.project.id, target.container.ID)
      : await projects.logs(target.project.id)
    if (logTarget.value === target) log.value = result.log
  } catch (e) {
    if (logTarget.value === target) logError.value = errorText(e, '读取日志失败')
  } finally {
    if (logTarget.value === target) logLoading.value = false
  }
}

function openLogs(project: Project, container?: ProjectContainer) {
  logTarget.value = { project, container }
  log.value = ''
  logLoading.value = false
  logOpen.value = true
  void refreshLogs()
}
// 绑定域名
const binding = ref(false)
const preset = ref<BindPreset | null>(null)
function bind(item: Project) { preset.value = { kind: 'project', id: item.id, name: item.name }; binding.value = true }
async function bound(result: BindResult) {
  if (result.operation) { if (await track(result.operation, `为 ${result.domain} 申请证书`, `${result.domain} 证书已启用`)) await load(); return }
  ElMessage.success('域名已绑定'); await load()
}
const editorTitle = computed(() => selected.value ? `编辑 ${selected.value.name}` : '新建容器项目')
</script>
<template>
  <PageHeader title="容器项目" description="用 Docker Compose 部署和管理应用。">
    <el-button :disabled="busy || loading" @click="reload"><AppIcon name="refresh"/>刷新</el-button>
    <el-button type="primary" :disabled="busy" @click="edit()"><AppIcon name="plus"/>新建项目</el-button>
  </PageHeader>
  <OperationBanner :operation="operation" :label="operationLabel" @close="operation = null"/>
  <div class="panel">
    <ListState :loading="loading" :error="loadError" :empty="!items.length" icon="projects" title="还没有容器项目" description="新建项目时粘贴 Compose 配置，面板会自动部署。" @retry="reload">
      <template #empty-action><el-button type="primary" :disabled="busy" @click="edit()"><AppIcon name="plus"/>新建项目</el-button></template>
      <div class="tbl-wrap">
        <table class="rtable project-table">
          <thead><tr><th scope="col">项目</th><th scope="col">运行状态</th><th scope="col">配置更新时间</th><th scope="col"><span class="sr">操作</span></th></tr></thead>
          <tbody>
            <template v-for="{ item, runtime, containers, hasActiveContainer, summary } in rows" :key="item.id">
              <tr>
                <td class="first">
                  <div class="project-name">
                    <button
                      type="button"
                      class="icon-btn project-expand"
                      :aria-expanded="expanded.has(item.id)"
                      :aria-controls="`project-containers-${item.id}`"
                      :aria-label="`${expanded.has(item.id) ? '收起' : '展开'} ${item.name} 的容器`"
                      :title="expanded.has(item.id) ? '收起容器' : '展开容器'"
                      @click="expanded.has(item.id) ? expanded.delete(item.id) : expanded.add(item.id)"
                    ><AppIcon name="chevron"/></button>
                    <div>
                      <span class="name">{{ item.name }}</span>
                      <span v-for="b in domainsOf(item.id)" :key="b.id" class="sub">{{ b.domain }}</span>
                      <span v-if="!domainsOf(item.id).length" class="sub">未绑定域名</span>
                    </div>
                  </div>
                </td>
                <td data-label="运行状态"><span :class="['pill', summary.tone]">{{ summary.label }}</span></td>
                <td data-label="配置更新" class="muted num">{{ formatTime(item.updated_at) }}</td>
                <td class="actions">
                  <span class="row-act">
                    <el-button size="small" :disabled="busy || runtime?.state !== 'ready'" @click="action(item, hasActiveContainer ? 'stop' : 'start')">
                      <AppIcon :name="hasActiveContainer ? 'stop' : 'play'"/>{{ hasActiveContainer ? '停止' : '启动' }}
                    </el-button>
                    <el-button size="small" :disabled="busy || !containers.length" @click="action(item, 'restart')"><AppIcon name="restart"/>重启</el-button>
                    <el-button size="small" :disabled="busy" @click="edit(item)"><AppIcon name="edit"/>编辑</el-button>
                    <RowMenu :items="menu(item)" :label="item.name" :disabled="busy"/>
                  </span>
                </td>
              </tr>
              <tr v-show="expanded.has(item.id)" :id="`project-containers-${item.id}`" class="project-detail">
                <td colspan="4">
                  <template v-if="expanded.has(item.id)">
                    <p v-if="!runtime || runtime.state === 'loading'" class="ports-empty" role="status">正在读取容器状态…</p>
                    <p v-else-if="runtime.state === 'failed'" class="ports-empty" role="alert">{{ runtime.message }}</p>
                    <p v-else-if="!containers.length" class="ports-empty">暂无容器</p>
                    <div v-else class="ports">
                      <table class="rtable container-table" :aria-label="`${item.name} 的容器`">
                        <thead><tr><th scope="col">容器</th><th scope="col">服务</th><th scope="col">状态</th><th scope="col">健康检查</th><th scope="col"><span class="sr">操作</span></th></tr></thead>
                        <tbody>
                          <tr v-for="container in containers" :key="container.ID || container.Name">
                            <td data-label="容器" class="mono">{{ container.Name }}</td>
                            <td data-label="服务">{{ container.Service }}</td>
                            <td data-label="状态">
                              <span v-if="container.State === 'exited' && container.ExitCode !== 0" class="pill bad">已退出（代码 {{ container.ExitCode }}）</span>
                              <span v-else :class="['pill', containerStates[container.State]?.tone ?? 'idle']">{{ containerStates[container.State]?.label ?? '状态未知' }}</span>
                            </td>
                            <td data-label="健康检查" :class="{ 'health-unhealthy': container.Health === 'unhealthy' }">{{ healthLabels[container.Health] ?? '—' }}</td>
                            <td class="actions"><el-button size="small" :disabled="!container.ID" @click="openLogs(item, container)">查看日志</el-button></td>
                          </tr>
                        </tbody>
                      </table>
                    </div>
                  </template>
                </td>
              </tr>
            </template>
          </tbody>
        </table>
      </div>
    </ListState>
  </div>
  <el-drawer v-model="editor" :title="editorTitle" size="min(720px, 100vw)" :close-on-click-modal="false" append-to-body class="app-drawer">
    <template #header><div><h2>{{ editorTitle }}</h2><p v-if="selected">保存后将重新部署项目。</p></div></template>
    <div v-loading="editorLoading" class="drawer-body">
      <el-alert v-if="editorError" :title="editorError" type="error" :closable="false" show-icon/>
      <el-form ref="formRef" :model="form" :rules="rules" label-position="top" @submit.prevent="save">
        <el-form-item label="项目名称" prop="name"><el-input v-model="form.name" maxlength="100" placeholder="例如 ghost-blog"/></el-form-item>
        <el-form-item label="Compose 配置" prop="compose"><el-input v-model="form.compose" type="textarea" :rows="13" spellcheck="false" placeholder="粘贴 Compose YAML 配置"/></el-form-item>
        <el-form-item label="环境变量 .env（可选）"><el-input v-model="form.environment" type="textarea" :rows="4" spellcheck="false" placeholder="每行填写 KEY=VALUE"/></el-form-item>
      </el-form>
      <section class="block">
        <div class="block-h"><h3>服务端口</h3><el-button size="small" :loading="scanning" :disabled="editorLoading" @click="scan"><AppIcon v-if="!scanning" name="refresh"/>扫描端口</el-button></div>
        <div class="ports">
          <table v-if="ports.length"><thead><tr><th>服务</th><th>容器端口</th><th>宿主机</th><th>来源</th></tr></thead><tbody><tr v-for="(p, index) in ports" :key="index"><td>{{ p.service }}</td><td class="mono">{{ p.container_port }}/{{ p.protocol }}</td><td :class="p.source === 'expose' ? 'muted' : 'mono'">{{ hostPort(p) }}</td><td><span class="tag">{{ p.source }}</span></td></tr></tbody></table>
          <div v-else class="ports-empty">{{ scanned ? 'Compose 配置中没有声明端口。' : '从 Compose 配置中识别可绑定域名的端口。' }}</div>
        </div>
      </section>
    </div>
    <template #footer><el-button @click="editor = false">取消</el-button><el-button type="primary" :loading="saving" :disabled="editorLoading" @click="save">{{ selected ? '保存配置' : '创建' }}</el-button></template>
  </el-drawer>
  <el-drawer v-model="logOpen" :title="logTitle" size="min(720px, 100vw)" append-to-body class="app-drawer">
    <div class="drawer-body">
      <section class="block">
        <div class="block-h">
          <h3>最近日志</h3>
          <el-button size="small" :loading="logLoading" @click="refreshLogs"><AppIcon v-if="!logLoading" name="refresh"/>刷新</el-button>
        </div>
        <el-alert v-if="logError" :title="logError" type="error" :closable="false" show-icon/>
        <pre v-else-if="log" class="log tall" tabindex="0">{{ log }}</pre>
        <p v-else class="ports-empty" role="status">{{ logLoading ? '正在读取日志…' : '暂无日志' }}</p>
      </section>
    </div>
    <template #footer><el-button @click="logOpen = false">关闭</el-button></template>
  </el-drawer>
  <el-dialog v-model="deleteOpen" :title="`删除 ${deleteTarget?.name ?? ''}`" width="min(520px, calc(100vw - 32px))" :close-on-click-modal="false" append-to-body @opened="cancelDelete?.ref?.focus()">
    <p>删除项目会移除容器、网络和域名绑定，保留 Docker 数据卷及镜像。</p>
    <el-checkbox v-model="deleteFiles" :disabled="!deleteTarget?.directory" :aria-describedby="deleteFiles ? 'project-delete-directory' : undefined">同时删除项目文件</el-checkbox>
    <div v-if="deleteFiles" id="project-delete-directory" class="project-delete-directory">
      <p>以下目录将被永久删除，包括配置、环境变量文件和目录内的挂载数据（如 <code>./data</code>）。</p>
      <code>{{ deleteTarget?.directory }}</code>
    </div>
    <template #footer>
      <el-button ref="cancelDelete" @click="deleteOpen = false">取消</el-button>
      <el-button type="danger" :disabled="busy" @click="confirmDelete">删除</el-button>
    </template>
  </el-dialog>
  <BindDomainDialog v-model:open="binding" :preset="preset" @done="bound"/>
</template>
