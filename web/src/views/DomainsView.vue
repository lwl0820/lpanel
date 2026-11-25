<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import type { Operation } from '../api'
import { acme, type ACMEAccount } from '../acme-api'
import { domains, isWildcardHostname, type Binding } from '../domain-api'
import { projects, type Project } from '../projects-api'
import { programs, type Program } from '../programs-api'
import { sites, type Site } from '../sites-api'
import { certificateActionLabel, certState, daysLeft } from '../cert'
import { useOperation } from '../useOperation'
import { errorText, formatDate } from '../format'
import AppIcon from '../components/AppIcon.vue'
import PageHeader from '../components/PageHeader.vue'
import ListState from '../components/ListState.vue'
import CertPill from '../components/CertPill.vue'
import RowMenu, { type MenuItem } from '../components/RowMenu.vue'
import OperationBanner from '../components/OperationBanner.vue'
import BindDomainDialog, { type BindResult } from '../components/BindDomainDialog.vue'
import RequestCertificateDialog, { type CertificateRequestResult } from '../components/RequestCertificateDialog.vue'
const items = ref<Binding[]>([])
const projectList = ref<Project[]>([])
const programList = ref<Program[]>([])
const siteList = ref<Site[]>([])
const accounts = ref<ACMEAccount[]>([])
const accountsById = computed(() => new Map(accounts.value.map(account => [account.id, account])))
const loading = ref(true)
const loadError = ref('')
const now = ref(new Date())
const { operation, label: operationLabel, busy, track } = useOperation(domains.operation)
// 面板域名变更后显示新地址，证书生效后旧入口可能不可用
const accessURL = ref('')
async function load() {
  loadError.value = ''
  try {
    const [bindings, projectData, siteData, accountData, programData] = await Promise.all([domains.list(), projects.list(), sites.list(), acme.list(), programs.list()])
    items.value = bindings
    projectList.value = projectData
    siteList.value = siteData
    programList.value = programData.programs
    accounts.value = accountData.accounts
    now.value = new Date()
  } catch (e) { loadError.value = errorText(e, '读取域名失败') } finally { loading.value = false }
}
onMounted(load)
async function reload() { loading.value = true; await load() }
const KIND = { panel: '面板', project: '编排项目', site: '静态网站', program: '程序' }
function target(b: Binding) {
  if (b.kind === 'panel') return { name: '本面板', detail: '' }
  if (b.kind === 'program') return {
    name: programList.value.find(p => p.id === b.resource_id)?.name ?? '已删除的程序',
    detail: b.upstream,
  }
  if (b.kind === 'project') return {
    name: projectList.value.find(p => p.id === b.resource_id)?.name ?? '已删除的项目',
    detail: b.host_port
      ? b.service ? `宿主机监听 ${b.service} · ${b.upstream}` : `自定义端口 ${b.host_port}`
      : `${b.service}:${b.container_port}`,
  }
  const site = siteList.value.find(s => s.id === b.resource_id)
  return { name: site?.identifier ?? '已删除的网站', detail: b.directory || site?.directory || '' }
}
function certificateAccount(b: Binding) {
  const account = accountsById.value.get(b.certificate_account_id ?? '')
  return account ? `${account.name} · ${account.provider_name}` : ''
}
async function follow(op: Operation, domain: string) {
  const result = await track(op, `为 ${domain} 申请证书`, `${domain} 证书已启用`, accessURL.value ? '访问入口可能已变更，请打开上方新地址确认结果。' : undefined)
  if (result) await load()
}
const certificateDialog = ref(false)
const certificateBinding = ref<Binding | null>(null)
function certificate(b: Binding) {
  certificateBinding.value = b
  certificateDialog.value = true
}
async function certificateDone(result: CertificateRequestResult) {
  if (result.accessURL) accessURL.value = result.accessURL
  await follow(result.operation, result.binding.domain)
}
async function remove(b: Binding) {
  try { await ElMessageBox.confirm(`解除 ${b.domain} 的域名绑定？`, '解除绑定', { type: 'warning', confirmButtonText: '解除', cancelButtonText: '取消', confirmButtonClass: 'el-button--danger' }) } catch { return }
  busy.value = true
  try { await domains.remove(b.id); ElMessage.success('已解除绑定'); await load() } catch (e) { ElMessage.error(errorText(e, '解除失败')) } finally { busy.value = false }
}
const menu = (b: Binding): MenuItem[] => [
  ...(isWildcardHostname(b.domain) ? [] : [{ label: certificateActionLabel(b), icon: 'cert', onSelect: () => certificate(b) } satisfies MenuItem]),
  { label: '编辑', icon: 'edit', onSelect: () => edit(b) },
  ...(b.kind === 'panel' ? [] : [{ label: '解除绑定', icon: 'trash', danger: true, divided: true, onSelect: () => remove(b) } satisfies MenuItem]),
]
// 绑定 / 编辑对话框
const dialog = ref(false)
const editing = ref<Binding | null>(null)
const editingProjectPorts = computed(() => editing.value?.kind === 'project'
  ? projectList.value.find(project => project.id === editing.value?.resource_id)?.custom_ports
  : undefined)
function edit(b?: Binding) { editing.value = b ?? null; dialog.value = true }
async function done(result: BindResult) {
  if (result.accessURL) accessURL.value = result.accessURL
  if (result.operation) { await follow(result.operation, result.domain); return }
  ElMessage.success(editing.value ? '域名已保存' : '域名已绑定'); await load()
}
</script>
<template>
  <PageHeader title="域名与证书" description="把域名指向面板、项目、程序或网站，并自动申请 HTTPS 证书。证书到期前 30 天会标记提醒。">
    <el-button @click="$router.push('/domains/acme-accounts')"><AppIcon name="cert"/>ACME 账户管理</el-button>
    <el-button :disabled="busy || loading" @click="reload"><AppIcon name="refresh"/>刷新</el-button>
    <el-button type="primary" :disabled="busy" @click="edit()"><AppIcon name="plus"/>绑定域名</el-button>
  </PageHeader>
  <div v-if="accessURL" class="banner info" role="status">
    <AppIcon name="link"/><span class="txt">面板访问地址已变更，证书生效后请使用新地址：<a :href="accessURL" class="mono">打开面板新地址 {{ accessURL }}</a></span>
    <button type="button" class="icon-btn" aria-label="关闭" title="关闭" @click="accessURL = ''"><AppIcon name="x"/></button>
  </div>
  <OperationBanner :operation="operation" :label="operationLabel" @close="operation = null"/>
  <div class="panel">
    <ListState :loading="loading" :error="loadError" :empty="!items.length" icon="domains" title="还没有绑定域名" description="先把域名的 DNS 解析到本服务器，再在这里绑定。" @retry="reload">
      <template #empty-action><el-button type="primary" @click="edit()"><AppIcon name="plus"/>绑定域名</el-button></template>
      <div class="tbl-wrap">
        <table class="rtable">
          <thead><tr><th>域名</th><th>指向</th><th>证书</th><th>到期时间</th><th><span class="sr">操作</span></th></tr></thead>
          <tbody>
            <tr v-for="b in items" :key="b.id">
              <td class="first"><span class="name">{{ b.domain }}</span><span class="sub"><span class="tag">{{ KIND[b.kind] }}</span></span></td>
              <td data-label="指向">{{ target(b).name }}<span v-if="target(b).detail" :class="['muted', { mono: b.kind === 'site' }]"> · {{ target(b).detail }}</span></td>
              <td data-label="证书">
                <span v-if="isWildcardHostname(b.domain)" class="tag">仅 HTTP</span>
                <span v-else><CertPill :state="certState(b, now)"/><span v-if="b.certificate_account_id" class="sub">{{ certificateAccount(b) }}</span><span v-if="b.certificate_error" class="err-line">{{ b.certificate_error }}</span></span>
              </td>
              <td data-label="到期" class="num"><template v-if="b.expires_at && b.certificate_status !== 'failed' && daysLeft(b.expires_at, now) !== null">{{ formatDate(b.expires_at) }}<span class="sub">剩 {{ daysLeft(b.expires_at, now) }} 天</span></template><span v-else class="muted">—</span></td>
              <td class="actions"><span class="row-act"><RowMenu :items="menu(b)" :label="b.domain" :disabled="busy"/></span></td>
            </tr>
          </tbody>
        </table>
      </div>
    </ListState>
  </div>
  <BindDomainDialog v-model:open="dialog" :binding="editing" :editing-project-ports="editingProjectPorts" @done="done"/>
  <RequestCertificateDialog v-model:open="certificateDialog" :binding="certificateBinding" @done="certificateDone"/>
</template>
