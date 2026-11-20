<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage, ElMessageBox, type FormInstance, type FormRules } from 'element-plus'
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
async function load() {
  loadError.value = ''
  try { [items.value, bindings.value] = await Promise.all([projects.list(), domains.list()]) } catch (e) { loadError.value = errorText(e, '读取项目失败') } finally { loading.value = false }
}
// 概览“开始使用”跳转时带 ?new=1，直接打开新建
const route = useRoute()
const router = useRouter()
onMounted(() => { void load(); if (route.query.new) { void edit(); void router.replace('/projects') } })
async function reload() { loading.value = true; await load() }
const domainsOf = (id: string) => bindings.value.filter(b => b.kind === 'project' && b.resource_id === id)
const verbs: Record<string, [string, string]> = { start: ['启动', '已启动'], stop: ['停止', '已停止'], restart: ['重启', '已重启'], delete: ['删除', '已删除'] }
async function action(item: Project, name: string) {
  if (name === 'delete') { try { await ElMessageBox.confirm('删除项目将移除容器和域名绑定，数据卷及挂载目录会保留。', `删除 ${item.name}`, { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消', confirmButtonClass: 'el-button--danger' }) } catch { return } }
  busy.value = true
  try { if (await track(await projects.action(item.id, name), `${verbs[name][0]} ${item.name}`, `${item.name} ${verbs[name][1]}`)) await load() } catch (e) { busy.value = false; ElMessage.error(errorText(e, '操作失败')) }
}
const menu = (item: Project): MenuItem[] => [
  { label: '启动', icon: 'play', onSelect: () => action(item, 'start') },
  { label: '停止', icon: 'stop', onSelect: () => action(item, 'stop') },
  { label: '重启', icon: 'restart', onSelect: () => action(item, 'restart') },
  { label: '状态与日志', icon: 'log', divided: true, onSelect: () => inspect(item) },
  { label: '绑定域名', icon: 'link', onSelect: () => bind(item) },
  { label: '删除项目', icon: 'trash', danger: true, divided: true, onSelect: () => action(item, 'delete') },
]
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
      void track(result.operation, `部署 ${result.name}`, `${result.name} 部署完成`)
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
// 状态与日志抽屉
const inspection = ref(false)
const inspecting = ref(false)
const inspectError = ref('')
const containers = ref<ProjectContainer[]>([])
const log = ref('')
async function refreshInspection() {
  if (!selected.value) return
  inspecting.value = true; inspectError.value = ''
  try { const [state, logs] = await Promise.all([projects.status(selected.value.id), projects.logs(selected.value.id)]); containers.value = state; log.value = logs.log } catch (e) { inspectError.value = errorText(e, '读取状态失败') } finally { inspecting.value = false }
}
function inspect(item: Project) { selected.value = item; containers.value = []; log.value = ''; inspection.value = true; void refreshInspection() }
const statePill = (c: ProjectContainer) => c.State === 'running' ? 'ok' : c.State === 'restarting' ? 'warn' : c.State === 'exited' && c.ExitCode !== 0 ? 'bad' : 'idle'
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
  <PageHeader title="容器项目" description="用 Docker Compose 部署和管理应用。运行状态在“状态与日志”中查看。">
    <el-button :disabled="busy || loading" @click="reload"><AppIcon name="refresh"/>刷新</el-button>
    <el-button type="primary" :disabled="busy" @click="edit()"><AppIcon name="plus"/>新建项目</el-button>
  </PageHeader>
  <OperationBanner :operation="operation" :label="operationLabel" @close="operation = null"/>
  <div class="panel">
    <ListState :loading="loading" :error="loadError" :empty="!items.length" icon="projects" title="还没有容器项目" description="新建项目时粘贴 Compose 配置，面板会自动部署。" @retry="reload">
      <template #empty-action><el-button type="primary" :disabled="busy" @click="edit()"><AppIcon name="plus"/>新建项目</el-button></template>
      <div class="tbl-wrap">
        <table class="rtable">
          <thead><tr><th>项目</th><th>配置更新时间</th><th><span class="sr">操作</span></th></tr></thead>
          <tbody>
            <tr v-for="item in items" :key="item.id">
              <td class="first"><span class="name">{{ item.name }}</span><span v-for="b in domainsOf(item.id)" :key="b.id" class="sub">{{ b.domain }}</span><span v-if="!domainsOf(item.id).length" class="sub">未绑定域名</span></td>
              <td data-label="配置更新" class="muted num">{{ formatTime(item.updated_at) }}</td>
              <td class="actions"><span class="row-act"><el-button size="small" :disabled="busy" @click="edit(item)"><AppIcon name="edit"/>编辑配置</el-button><RowMenu :items="menu(item)" :label="item.name" :disabled="busy"/></span></td>
            </tr>
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
  <el-drawer v-model="inspection" :title="`${selected?.name ?? ''} · 状态与日志`" size="min(720px, 100vw)" append-to-body class="app-drawer">
    <template #header><div><h2>{{ selected?.name }} · 状态与日志</h2><p>容器运行状态与最近的日志。</p></div></template>
    <div class="drawer-body">
      <el-alert v-if="inspectError" :title="inspectError" type="error" :closable="false" show-icon/>
      <section class="block">
        <h3>容器</h3>
        <div class="ports">
          <table v-if="containers.length"><thead><tr><th>服务</th><th>容器</th><th>状态</th><th>健康检查</th></tr></thead><tbody><tr v-for="c in containers" :key="c.Name"><td>{{ c.Service }}</td><td class="mono">{{ c.Name }}</td><td><span :class="['pill', statePill(c)]">{{ c.State }}</span></td><td>{{ c.Health || '—' }}</td></tr></tbody></table>
          <div v-else class="ports-empty">{{ inspecting ? '正在读取…' : '暂无运行记录' }}</div>
        </div>
      </section>
      <section class="block">
        <div class="block-h"><h3>日志</h3><el-button size="small" :loading="inspecting" @click="refreshInspection"><AppIcon v-if="!inspecting" name="refresh"/>刷新</el-button></div>
        <pre class="log tall">{{ log || (inspecting ? '正在读取…' : '暂无日志') }}</pre>
      </section>
    </div>
    <template #footer><el-button @click="inspection = false">关闭</el-button></template>
  </el-drawer>
  <BindDomainDialog v-model:open="binding" :preset="preset" @done="bound"/>
</template>
