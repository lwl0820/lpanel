<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { projects, type Project } from '../projects-api'
import { sites, type Site } from '../sites-api'
import { domains, type Access, type Binding } from '../domain-api'
import { upgrades, type UpgradeState } from '../upgrade-api'
import { certState, overviewTodos } from '../cert'
import { useOperation } from '../useOperation'
import { errorText, formatDate, formatTime } from '../format'
import AppIcon from '../components/AppIcon.vue'
import PageHeader from '../components/PageHeader.vue'
import ListState from '../components/ListState.vue'
import OperationBanner from '../components/OperationBanner.vue'
import RequestCertificateDialog, { type CertificateRequestResult } from '../components/RequestCertificateDialog.vue'
interface Data { projects: Project[]; sites: Site[]; bindings: Binding[]; access: Access; upgrade: UpgradeState }
const data = ref<Data | null>(null)
const loading = ref(true)
const loadError = ref('')
const now = ref(new Date())
const { operation, label: operationLabel, busy, track } = useOperation(domains.operation)
// 任一接口失败都整块显示错误，不用 0 冒充真实数据
async function load() {
  loadError.value = ''
  try {
    const [p, s, b, access, upgrade] = await Promise.all([projects.list(), sites.list(), domains.list(), domains.access(), upgrades.status()])
    data.value = { projects: p, sites: s, bindings: b, access, upgrade }; now.value = new Date()
  } catch (e) { data.value = null; loadError.value = errorText(e, '读取概览失败') } finally { loading.value = false }
}
onMounted(load)
async function reload() { loading.value = true; await load() }
const bound = (kind: Binding['kind'], id: string) => data.value?.bindings.some(b => b.kind === kind && b.resource_id === id)
const todos = computed(() => data.value ? overviewTodos(data.value.bindings, data.value.upgrade, now.value) : [])
const certTodos = computed(() => todos.value.filter(t => t.kind !== 'upgrade').length)
const stats = computed(() => {
  const d = data.value
  if (!d) return []
  const failed = d.bindings.some(b => certState(b, now.value) === 'failed')
  return [
    { to: '/projects', label: '容器项目', value: d.projects.length, hint: d.projects.length ? `${d.projects.filter(p => bound('project', p.id)).length} 个已绑定域名` : '尚未添加', tone: '' },
    { to: '/sites', label: '静态网站', value: d.sites.length, hint: d.sites.length ? `${d.sites.filter(s => s.spa).length} 个单页应用` : '尚未添加', tone: '' },
    { to: '/domains', label: '域名', value: d.bindings.length, hint: d.bindings.length ? `${d.bindings.filter(b => b.certificate_status === 'enabled').length} 个已启用 HTTPS` : '尚未绑定', tone: '' },
    { to: '/domains', label: '证书需处理', value: certTodos.value, hint: certTodos.value ? '失败或 30 天内到期' : '无', tone: certTodos.value ? (failed ? 'bad' : 'warn') : '' },
  ]
})
const fresh = computed(() => !!data.value && !data.value.projects.length && !data.value.sites.length)
const url = computed(() => data.value ? `${data.value.access.https ? 'https' : 'http'}://${data.value.access.panel_domain || window.location.hostname}:${data.value.access.panel_port}` : '')
const certificateDialog = ref(false)
const certificateBinding = ref<Binding | null>(null)
function certificate(b: Binding) {
  certificateBinding.value = b
  certificateDialog.value = true
}
async function certificateDone({ binding, operation }: CertificateRequestResult) {
  const result = await track(operation, `为 ${binding.domain} 申请证书`, `${binding.domain} 证书已启用`, binding.kind === 'panel' ? '访问入口可能已变更，请到“域名与证书”确认结果。' : undefined)
  if (result) await load()
}
</script>
<template>
  <PageHeader title="概览" description="服务器上的项目、网站和需要处理的事项。"/>
  <OperationBanner :operation="operation" :label="operationLabel" @close="operation = null"/>
  <div v-if="loading || loadError" class="panel"><ListState :loading="loading" :error="loadError" @retry="reload"/></div>
  <template v-else-if="data">
    <nav class="panel stats" aria-label="资源统计">
      <RouterLink v-for="s in stats" :key="s.label" :to="s.to" class="stat"><span class="lbl">{{ s.label }}</span><span :class="['val', s.tone]">{{ s.value }}</span><span class="hint">{{ s.hint }}</span></RouterLink>
    </nav>
    <div class="grid2">
      <section v-if="fresh" class="panel">
        <div class="panel-h"><h2>开始使用</h2></div>
        <ul class="todo">
          <li><span class="dot info">1</span><span class="txt"><b>添加容器项目</b><span>粘贴 Compose 配置，一键部署。</span></span><el-button size="small" type="primary" @click="$router.push('/projects?new=1')">新建项目</el-button></li>
          <li><span class="dot info">2</span><span class="txt"><b>或添加静态网站</b><span>指定服务器上的网页目录。</span></span><el-button size="small" @click="$router.push('/sites?new=1')">新建网站</el-button></li>
          <li><span class="dot info">3</span><span class="txt"><b>绑定域名并申请证书</b><span>先把域名解析到本服务器。</span></span><el-button size="small" @click="$router.push('/domains')">去绑定</el-button></li>
        </ul>
      </section>
      <section v-else class="panel">
        <div class="panel-h"><h2>需要处理</h2><span class="muted">{{ todos.length }} 项</span></div>
        <ul class="todo">
          <li v-for="t in todos" :key="t.binding?.id ?? t.kind">
            <template v-if="t.kind === 'cert-failed' && t.binding"><span class="dot bad"><AppIcon name="alert"/></span><span class="txt"><b>{{ t.binding.domain }} 证书申请失败</b><span>{{ t.binding.certificate_error || '请检查域名解析和 80 端口后重试。' }}</span></span><el-button size="small" :disabled="busy" @click="certificate(t.binding)">重新申请</el-button></template>
            <template v-else-if="t.kind === 'cert-expiring' && t.binding"><span class="dot warn"><AppIcon name="clock"/></span><span class="txt"><b>{{ t.binding.domain }} 证书 {{ t.days }} 天后到期</b><span>到期日 {{ formatDate(t.binding.expires_at) }}，可提前续期</span></span><el-button size="small" :disabled="busy" @click="certificate(t.binding)">续期</el-button></template>
            <template v-else><span class="dot info"><AppIcon name="up"/></span><span class="txt"><b>可升级到 {{ data.upgrade.latest }}</b><span>当前 {{ data.upgrade.current }}<template v-if="data.upgrade.checked_at">，检查于 {{ formatTime(data.upgrade.checked_at) }}</template></span></span><el-button size="small" @click="$router.push('/settings')">查看</el-button></template>
          </li>
          <li v-if="!todos.length"><span class="dot ok"><AppIcon name="check"/></span><span class="txt"><b>一切正常</b><span>没有失败或即将到期的证书，面板已是最新版本。</span></span></li>
        </ul>
      </section>
      <section class="panel">
        <div class="panel-h"><h2>面板访问</h2></div>
        <div class="panel-b">
          <dl class="kv">
            <dt>访问地址</dt><dd class="mono">{{ url }}</dd>
            <dt>HTTPS</dt><dd><span :class="['pill', data.access.https ? 'ok' : 'idle']">{{ data.access.https ? '已启用' : '未启用' }}</span></dd>
            <dt>证书账户</dt><dd><RouterLink to="/domains/acme-accounts" class="link">管理证书账户</RouterLink></dd>
            <dt>版本</dt><dd>{{ data.upgrade.current }}</dd>
          </dl>
        </div>
        <div class="panel-f"><el-button size="small" @click="$router.push('/settings')">面板设置</el-button></div>
      </section>
    </div>
  </template>
  <RequestCertificateDialog v-model:open="certificateDialog" :binding="certificateBinding" @done="certificateDone"/>
</template>
