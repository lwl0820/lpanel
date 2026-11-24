<script setup lang="ts">
import { computed, reactive, ref, useId, watch } from 'vue'
import type { FormInstance, FormRules } from 'element-plus'
import type { Operation } from '../api'
import { domains, isWildcardHostname, panelLoginURL, type Binding } from '../domain-api'
import { projects, type Project, type ProjectBindingTarget, type ProjectRuntimePorts } from '../projects-api'
import { sites, type Site } from '../sites-api'
import { errorText } from '../format'
import AcmeAccountSelect from './AcmeAccountSelect.vue'
export type BindPreset = { id: string; name: string } & (
  { kind: 'project'; customPorts?: number[] } | { kind: 'site' }
)
export interface BindResult { operation: Operation | null; accessURL: string; domain: string }
interface PortOption { value: string; label: string; target: ProjectBindingTarget }
const currentPort = 'current'
// 新建时 preset 为空则先选目标，编辑时用 binding 回显设置
const props = defineProps<{ preset?: BindPreset | null; binding?: Binding | null; editingProjectPorts?: number[] }>()
const open = defineModel<boolean>('open', { required: true })
const emit = defineEmits<{ done: [result: BindResult] }>()
const form = reactive({ target: '', domain: '', port: '', scheme: 'http', auto_certificate: false, accountId: '' })
const formId = useId()
const domainHintId = useId()
const formRef = ref<FormInstance>()
const projectList = ref<Project[]>([])
const siteList = ref<Site[]>([])
const runtime = ref<ProjectRuntimePorts>()
const scanning = ref(false)
const scanError = ref('')
const saving = ref(false)
const error = ref('')
const kind = computed(() => props.binding?.kind ?? form.target.split(':')[0])
const editingProject = computed(() => props.binding?.kind === 'project' ? props.binding : null)
const targetId = computed(() => editingProject.value?.resource_id ?? form.target.split(':')[1] ?? '')
const portGroups = computed(() => {
  const published: PortOption[] = []
  const listeners: PortOption[] = []
  for (const port of runtime.value?.ports ?? []) {
    const { service, host_ip, host_port } = port
    const address = `${host_ip.includes(':') && !host_ip.startsWith('[') ? `[${host_ip}]` : host_ip}:${host_port}`
    if (port.source === 'published') {
      published.push({
        value: `published:${published.length}`,
        label: `${service} · ${port.container_port}/TCP → ${address}`,
        target: { service, container_port: port.container_port },
      })
    } else {
      listeners.push({
        value: `host:${listeners.length}`,
        label: `${service} · ${address}`,
        target: { listen_target: { service, host_ip, host_port } },
      })
    }
  }
  const customPorts = editingProject.value ? props.editingProjectPorts : props.preset?.kind === 'project'
    ? props.preset.customPorts
    : projectList.value.find(project => project.id === targetId.value)?.custom_ports
  const custom = (customPorts ?? []).map(port => ({
    value: `custom:${port}`, label: `自定义端口 ${port}`, target: { host_port: port },
  }))
  return [
    { label: '已发布 TCP 端口', options: published },
    { label: '宿主机网络监听', options: listeners },
    { label: '自定义端口', options: custom },
  ].filter(group => group.options.length)
})
const selectedPort = computed(() => portGroups.value.flatMap(group => group.options).find(option => option.value === form.port))
const choosing = computed(() => !props.binding && !props.preset)
const wildcardSite = computed(() => kind.value === 'site' && isWildcardHostname(form.domain.trim()))
const requestsCertificate = computed(() => !wildcardSite.value && form.auto_certificate && (
  props.binding?.certificate_status !== 'enabled' ||
  props.binding.domain !== form.domain.trim().toLowerCase().replace(/\.$/, '')
))
const rules: FormRules = {
  domain: [{ required: true, whitespace: true, message: '请填写域名', trigger: ['blur', 'change'] }],
  target: [{ required: true, message: '请选择指向', trigger: 'change' }],
  port: [{ required: true, message: '请选择服务端口', trigger: 'change' }],
}
watch(open, async (value, _, onCleanup) => {
  if (!value) return
  error.value = ''
  Object.assign(form, {
    target: '', domain: props.binding?.domain ?? '',
    scheme: editingProject.value?.upstream.toLowerCase().startsWith('https://') ? 'https' : 'http',
    auto_certificate: props.binding?.auto_certificate ?? false, accountId: '',
  })
  formRef.value?.clearValidate()
  if (props.preset) { form.target = `${props.preset.kind}:${props.preset.id}`; return }
  if (props.binding) return
  let active = true
  onCleanup(() => { active = false })
  projectList.value = []
  siteList.value = []
  try {
    const [availableProjects, availableSites] = await Promise.all([projects.list(), sites.list()])
    if (!active) return
    projectList.value = availableProjects
    siteList.value = availableSites
    form.target = projectList.value[0] ? `project:${projectList.value[0].id}` : siteList.value[0] ? `site:${siteList.value[0].id}` : 'panel'
  } catch (e) { if (active) error.value = errorText(e, '读取项目和网站失败') }
})
watch(requestsCertificate, value => { if (!value) form.accountId = '' })
watch([open, kind, targetId], async ([isOpen, targetKind, projectId], _, onCleanup) => {
  form.port = editingProject.value ? currentPort : ''
  runtime.value = undefined
  scanError.value = ''
  scanning.value = false
  if (!isOpen || targetKind !== 'project') return

  let active = true
  onCleanup(() => { active = false })
  scanning.value = true
  try {
    const result = await projects.runtimePorts(projectId)
    if (active) runtime.value = result
  } catch (e) {
    if (active) scanError.value = errorText(e, '读取服务端口失败')
  } finally {
    if (active) scanning.value = false
  }
})
async function panelURL(domain: string) { try { return panelLoginURL(await domains.access(), domain, true) } catch { return '' } }
async function submit() {
  if (saving.value || (requestsCertificate.value && !form.accountId) || !await formRef.value?.validate().catch(() => false)) return
  saving.value = true; error.value = ''
  const domain = form.domain.trim()
  const certOptions = { auto_certificate: !wildcardSite.value && form.auto_certificate, acme_account_id: requestsCertificate.value ? form.accountId : undefined }
  try {
    let operation: Operation | null = null
    let accessURL = ''
    if (editingProject.value) {
      const port = selectedPort.value?.target
      if (form.port !== currentPort && !port) return
      operation = (await projects.updateBinding(targetId.value, editingProject.value.id, {
        domain, scheme: form.scheme, ...certOptions, ...port,
      })).operation
    } else if (props.binding) {
      const result = await domains.save({ ...props.binding, domain, ...certOptions })
      operation = result.operation
      if (result.binding.kind === 'panel' && operation) accessURL = await panelURL(result.binding.domain)
    } else if (kind.value === 'project') {
      if (!selectedPort.value) return
      operation = (await projects.bind(targetId.value, { domain, ...selectedPort.value.target, scheme: form.scheme, ...certOptions })).operation
    } else if (kind.value === 'site') {
      operation = (await sites.bind(targetId.value, { domain, ...certOptions })).operation
    } else {
      const result = await domains.save({ kind: 'panel', resource_id: 'panel', domain, ...certOptions })
      operation = result.operation
      if (operation) accessURL = await panelURL(result.binding.domain)
    }
    open.value = false
    emit('done', { operation, accessURL, domain })
  } catch (e) { error.value = errorText(e, '保存失败') } finally { saving.value = false }
}
</script>
<template>
  <el-dialog v-model="open" :title="binding ? `编辑 ${binding.domain}` : preset ? `为 ${preset.name} 绑定域名` : '绑定域名'" width="min(520px, calc(100vw - 32px))" :close-on-click-modal="false" append-to-body>
    <el-form :id="formId" ref="formRef" :model="form" :rules="rules" label-position="top" class="dialog-form" @submit.prevent="submit">
      <el-form-item label="域名" prop="domain">
        <el-input v-model="form.domain" placeholder="例如 blog.example.com" autocomplete="off" autocorrect="off" autocapitalize="none" spellcheck="false" :aria-describedby="domainHintId"/>
        <span :id="domainHintId" class="hint">请先将域名解析到本服务器。<template v-if="wildcardSite">通配符主机名仅支持 HTTP。</template></span>
      </el-form-item>
      <el-form-item v-if="choosing" label="指向" prop="target">
        <el-select v-model="form.target" placeholder="选择项目、网站或面板">
          <el-option-group v-if="projectList.length" label="编排项目"><el-option v-for="p in projectList" :key="p.id" :value="`project:${p.id}`" :label="p.name"/></el-option-group>
          <el-option-group v-if="siteList.length" label="静态网站"><el-option v-for="s in siteList" :key="s.id" :value="`site:${s.id}`" :label="s.identifier"/></el-option-group>
          <el-option-group label="其他"><el-option value="panel" label="本面板"/></el-option-group>
        </el-select>
      </el-form-item>
      <template v-if="kind === 'project'">
        <el-form-item label="服务端口" prop="port">
          <el-select v-model="form.port" placeholder="选择服务端口" :disabled="!editingProject && !portGroups.length">
            <el-option v-if="editingProject" :value="currentPort" label="保留当前端口"/>
            <el-option-group v-for="group in portGroups" :key="group.label" :label="group.label">
              <el-option v-for="port in group.options" :key="port.value" :value="port.value" :label="port.label"/>
            </el-option-group>
          </el-select>
          <span v-if="editingProject" class="hint">当前上游 {{ editingProject.upstream }}</span>
          <span v-if="scanning" class="hint" role="status">正在读取运行端口…</span>
        </el-form-item>
        <el-alert v-if="scanError || runtime?.warning" :title="scanError || runtime?.warning" type="warning" :closable="false" show-icon/>
        <p v-if="scanError || runtime?.warning" class="hint">重新打开此窗口可再次读取运行端口。</p>
        <p v-if="runtime && !runtime.running" class="hint" role="status">项目没有正在运行的容器。</p>
        <p v-else-if="runtime && !runtime.ports.length" class="hint" role="status">没有发现可绑定的运行端口。</p>
        <el-form-item label="上游协议"><el-radio-group v-model="form.scheme"><el-radio-button value="http">HTTP</el-radio-button><el-radio-button value="https">HTTPS</el-radio-button></el-radio-group></el-form-item>
      </template>
      <el-checkbox v-if="!wildcardSite" v-model="form.auto_certificate" name="auto_certificate">自动申请 HTTPS 证书</el-checkbox>
      <AcmeAccountSelect v-if="open && requestsCertificate" v-model="form.accountId"/>
      <p v-if="requestsCertificate" class="hint">申请证书需要公网 80 端口可达。</p>
      <el-alert v-if="error" :title="error" type="error" :closable="false" show-icon/>
    </el-form>
    <template #footer><el-button @click="open = false">取消</el-button><el-button type="primary" native-type="submit" :form="formId" :loading="saving" :disabled="(kind === 'project' && !binding && !portGroups.length) || (requestsCertificate && !form.accountId)">{{ binding ? '保存' : '绑定' }}</el-button></template>
  </el-dialog>
</template>
