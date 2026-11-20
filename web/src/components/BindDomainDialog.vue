<script setup lang="ts">
import { computed, reactive, ref, useId, watch } from 'vue'
import type { FormInstance, FormRules } from 'element-plus'
import type { Operation } from '../api'
import { domains, panelLoginURL, type Binding } from '../domain-api'
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
const formRef = ref<FormInstance>()
const projectList = ref<Project[]>([])
const siteList = ref<Site[]>([])
const ports = ref<ProjectPort[]>([])
const scanning = ref(false)
const saving = ref(false)
const error = ref('')
const kind = computed(() => props.binding?.kind ?? form.target.split(':')[0])
const targetId = computed(() => form.target.split(':')[1] ?? '')
const options = computed(() => ports.value.filter(p => p.source === 'ports' && p.protocol === 'tcp'))
const choosing = computed(() => !props.binding && !props.preset)
const requestsCertificate = computed(() => form.auto_certificate && (
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
watch(() => form.target, () => { form.port = ''; ports.value = []; if (open.value && kind.value === 'project' && !props.binding) void scan() })
async function scan() {
  const id = targetId.value
  scanning.value = true; error.value = ''
  try {
    const p = await projects.get(id)
    const result = await projects.scan({ id: p.id, name: p.name, compose: p.compose ?? '', environment: p.environment ?? '' })
    if (targetId.value === id) ports.value = result
  } catch (e) { error.value = errorText(e, '扫描端口失败') } finally { scanning.value = false }
}
async function panelURL(domain: string) { try { return panelLoginURL(await domains.access(), domain, true) } catch { return '' } }
async function submit() {
  if (saving.value || (requestsCertificate.value && !form.accountId) || !await formRef.value?.validate().catch(() => false)) return
  saving.value = true; error.value = ''
  const domain = form.domain.trim()
  const certOptions = { auto_certificate: form.auto_certificate, acme_account_id: requestsCertificate.value ? form.accountId : undefined }
  try {
    let operation: Operation | null = null
    let accessURL = ''
    if (props.binding) {
      const result = await domains.save({ ...props.binding, domain, ...certOptions })
      operation = result.operation
      if (result.binding.kind === 'panel' && operation) accessURL = await panelURL(result.binding.domain)
    } else if (kind.value === 'project') {
      const port = options.value[Number(form.port)]
      operation = (await projects.bind(targetId.value, { domain, service: port.service, container_port: port.container_port, scheme: form.scheme, ...certOptions })).operation
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
      <el-form-item label="域名" prop="domain"><el-input v-model="form.domain" placeholder="例如 blog.example.com" autocomplete="off"/><span class="hint">请先把该域名的 A 记录解析到本服务器。</span></el-form-item>
      <el-form-item v-if="choosing" label="指向" prop="target">
        <el-select v-model="form.target" placeholder="选择项目、网站或面板">
          <el-option-group v-if="projectList.length" label="容器项目"><el-option v-for="p in projectList" :key="p.id" :value="`project:${p.id}`" :label="p.name"/></el-option-group>
          <el-option-group v-if="siteList.length" label="静态网站"><el-option v-for="s in siteList" :key="s.id" :value="`site:${s.id}`" :label="s.name"/></el-option-group>
          <el-option-group label="其他"><el-option value="panel" label="本面板"/></el-option-group>
        </el-select>
      </el-form-item>
      <template v-if="kind === 'project' && !binding">
        <el-form-item label="服务端口" prop="port">
          <el-select v-model="form.port" :loading="scanning" :placeholder="scanning ? '正在扫描 Compose 配置…' : '选择已发布的 TCP 端口'" :disabled="scanning || !options.length">
            <el-option v-for="(port, index) in options" :key="index" :value="String(index)" :label="`${port.service} · ${port.container_port} → ${port.published || '自动分配'}`"/>
          </el-select>
          <span class="hint">来自项目 Compose 配置的端口扫描结果。</span>
        </el-form-item>
        <el-alert v-if="!scanning && !options.length && !error" title="没有可绑定的已发布 TCP 端口，请在 Compose 中添加 ports 映射。" type="info" :closable="false"/>
        <el-form-item label="上游协议"><el-radio-group v-model="form.scheme"><el-radio-button value="http">HTTP</el-radio-button><el-radio-button value="https">HTTPS</el-radio-button></el-radio-group></el-form-item>
      </template>
      <el-checkbox v-model="form.auto_certificate" name="auto_certificate">自动申请 HTTPS 证书</el-checkbox>
      <AcmeAccountSelect v-if="open && requestsCertificate" v-model="form.accountId"/>
      <p v-if="requestsCertificate" class="hint">申请证书需要公网 80 端口可达。</p>
      <el-alert v-if="error" :title="error" type="error" :closable="false" show-icon/>
    </el-form>
    <template #footer><el-button @click="open = false">取消</el-button><el-button type="primary" native-type="submit" :form="formId" :loading="saving" :disabled="scanning || (requestsCertificate && !form.accountId)">{{ binding ? '保存' : '绑定' }}</el-button></template>
  </el-dialog>
</template>
