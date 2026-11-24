<script setup lang="ts">
import { computed, reactive, ref, useId, watch } from 'vue'
import type { FormInstance, FormRules } from 'element-plus'
import type { Operation } from '../api'
import { domains, isWildcardHostname, panelLoginURL, type Binding } from '../domain-api'
import { projects, type Project, type ProjectPort } from '../projects-api'
import { sites, type Site } from '../sites-api'
import { errorText } from '../format'
import AcmeAccountSelect from './AcmeAccountSelect.vue'
export interface BindPreset { kind: 'project' | 'site'; id: string; name: string }
export interface BindResult { operation: Operation | null; accessURL: string; domain: string }
// 新建：preset 为空时先选目标（域名页）；编辑：传 binding，只改域名和自动证书
const props = defineProps<{ preset?: BindPreset | null; binding?: Binding | null }>()
const open = defineModel<boolean>('open', { required: true })
const emit = defineEmits<{ done: [result: BindResult] }>()
const form = reactive({ target: '', domain: '', port: '', scheme: 'http', auto_certificate: false, accountId: '' })
const formId = useId()
const domainHintId = useId()
const formRef = ref<FormInstance>()
const projectList = ref<Project[]>([])
const siteList = ref<Site[]>([])
const ports = ref<ProjectPort[]>([])
const customPorts = ref<number[]>([])
const scanning = ref(false)
const scanError = ref('')
const saving = ref(false)
const error = ref('')
const kind = computed(() => props.binding?.kind ?? form.target.split(':')[0])
const targetId = computed(() => form.target.split(':')[1] ?? '')
const options = computed(() => ports.value.filter(p => p.source === 'ports' && p.protocol === 'tcp'))
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
watch(open, async value => {
  if (!value) return
  error.value = ''; ports.value = []
  Object.assign(form, { target: '', domain: props.binding?.domain ?? '', port: '', scheme: 'http', auto_certificate: props.binding?.auto_certificate ?? false, accountId: '' })
  formRef.value?.clearValidate()
  if (props.preset) { form.target = `${props.preset.kind}:${props.preset.id}`; return }
  if (props.binding) return
  try {
    [projectList.value, siteList.value] = await Promise.all([projects.list(), sites.list()])
    form.target = projectList.value[0] ? `project:${projectList.value[0].id}` : siteList.value[0] ? `site:${siteList.value[0].id}` : 'panel'
  } catch (e) { error.value = errorText(e, '读取项目和网站失败') }
})
watch(requestsCertificate, value => { if (!value) form.accountId = '' })
watch([open, () => form.target], async ([isOpen], _, onCleanup) => {
  form.port = ''
  ports.value = []
  customPorts.value = []
  scanError.value = ''
  scanning.value = false
  if (!isOpen || kind.value !== 'project' || props.binding) return

  let active = true
  onCleanup(() => { active = false })
  const id = targetId.value
  scanning.value = true
  try {
    const p = await projects.get(id)
    if (!active) return
    customPorts.value = p.custom_ports ?? []
    const result = await projects.scan({ id: p.id, name: p.name, compose: p.compose ?? '', environment: p.environment ?? '' })
    if (active) ports.value = result
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
    if (props.binding) {
      const result = await domains.save({ ...props.binding, domain, ...certOptions })
      operation = result.operation
      if (result.binding.kind === 'panel' && operation) accessURL = await panelURL(result.binding.domain)
    } else if (kind.value === 'project') {
      const [source, value] = form.port.split(':')
      const port = options.value[Number(value)]
      const target = source === 'custom'
        ? { host_port: Number(value) }
        : { service: port.service, container_port: port.container_port }
      operation = (await projects.bind(targetId.value, { domain, ...target, scheme: form.scheme, ...certOptions })).operation
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
      <template v-if="kind === 'project' && !binding">
        <el-form-item label="服务端口" prop="port">
          <el-select v-model="form.port" placeholder="选择服务端口" :disabled="!options.length && !customPorts.length">
            <el-option-group v-if="options.length" label="已发布 TCP 端口">
              <el-option v-for="(port, index) in options" :key="index" :value="`published:${index}`" :label="`${port.service} · ${port.container_port} → ${port.published && port.published !== '0' ? port.published : '自动分配'}`"/>
            </el-option-group>
            <el-option-group v-if="customPorts.length" label="自定义端口">
              <el-option v-for="port in customPorts" :key="port" :value="`custom:${port}`" :label="`自定义端口 ${port}`"/>
            </el-option-group>
          </el-select>
          <span v-if="scanning" class="hint" role="status">正在扫描已发布端口…</span>
        </el-form-item>
        <el-alert v-if="scanError" :title="scanError" type="warning" :closable="false" show-icon/>
        <el-alert v-else-if="!scanning && !options.length && !customPorts.length" title="请在项目中添加自定义端口，或在 Compose 中发布 TCP 端口。" type="info" :closable="false"/>
        <el-form-item label="上游协议"><el-radio-group v-model="form.scheme"><el-radio-button value="http">HTTP</el-radio-button><el-radio-button value="https">HTTPS</el-radio-button></el-radio-group></el-form-item>
      </template>
      <el-checkbox v-if="!wildcardSite" v-model="form.auto_certificate" name="auto_certificate">自动申请 HTTPS 证书</el-checkbox>
      <AcmeAccountSelect v-if="open && requestsCertificate" v-model="form.accountId"/>
      <p v-if="requestsCertificate" class="hint">申请证书需要公网 80 端口可达。</p>
      <el-alert v-if="error" :title="error" type="error" :closable="false" show-icon/>
    </el-form>
    <template #footer><el-button @click="open = false">取消</el-button><el-button type="primary" native-type="submit" :form="formId" :loading="saving" :disabled="(kind === 'project' && !binding && !options.length && !customPorts.length) || (requestsCertificate && !form.accountId)">{{ binding ? '保存' : '绑定' }}</el-button></template>
  </el-dialog>
</template>
