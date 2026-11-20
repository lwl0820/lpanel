<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage, ElMessageBox, type FormInstance, type FormRules } from 'element-plus'
import { sites, type Site } from '../sites-api'
import { domains, type Binding } from '../domain-api'
import { useOperation } from '../useOperation'
import { errorText } from '../format'
import AppIcon from '../components/AppIcon.vue'
import PageHeader from '../components/PageHeader.vue'
import ListState from '../components/ListState.vue'
import RowMenu, { type MenuItem } from '../components/RowMenu.vue'
import OperationBanner from '../components/OperationBanner.vue'
import BindDomainDialog, { type BindPreset, type BindResult } from '../components/BindDomainDialog.vue'
import AcmeAccountSelect from '../components/AcmeAccountSelect.vue'
const items = ref<Site[]>([])
const bindings = ref<Binding[]>([])
const loading = ref(true)
const loadError = ref('')
const { operation, label: operationLabel, busy, track } = useOperation(domains.operation)
async function load(pendingBinding?: Binding) {
  loadError.value = ''
  try {
    const [siteList, bindingList] = await Promise.all([sites.list(), domains.list()])
    items.value = siteList
    // 证书操作完成前，刚创建的网站按绑定时的 HTTP 状态展示。
    bindings.value = pendingBinding ? bindingList.map(binding => binding.id === pendingBinding.id ? pendingBinding : binding) : bindingList
  } catch (e) {
    loadError.value = errorText(e, '读取网站失败')
  } finally {
    loading.value = false
  }
}
// 概览“开始使用”跳转时带 ?new=1，直接打开新建
const route = useRoute()
const router = useRouter()
onMounted(() => { void load(); if (route.query.new) { edit(); void router.replace('/sites') } })
async function reload() { loading.value = true; await load() }
const domainsOf = (id: string) => bindings.value.filter(b => b.kind === 'site' && b.resource_id === id)
async function remove(item: Site) {
  try { await ElMessageBox.confirm('删除网站将解除域名绑定，服务器上的内容目录会保留。', `删除 ${item.name}`, { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消', confirmButtonClass: 'el-button--danger' }) } catch { return }
  busy.value = true
  try { await sites.remove(item.id); ElMessage.success('网站已删除'); await load() } catch (e) { ElMessage.error(errorText(e, '删除失败')) } finally { busy.value = false }
}
const menu = (item: Site): MenuItem[] => [
  { label: '绑定域名', icon: 'link', onSelect: () => bind(item) },
  { label: '删除网站', icon: 'trash', danger: true, divided: true, onSelect: () => remove(item) },
]
// 网站对话框
const editor = ref(false)
const editorError = ref('')
const selected = ref<Site | null>(null)
const form = reactive({ name: '', directory: '', spa: false, domain: '', auto_certificate: false, accountId: '' })
const requestsCertificate = computed(() => !selected.value && Boolean(form.domain.trim()) && form.auto_certificate)
watch(requestsCertificate, value => { if (!value) form.accountId = '' })
const formRef = ref<FormInstance>()
const rules = computed<FormRules>(() => ({
  name: [{ required: true, whitespace: true, message: '请填写网站名称', trigger: ['blur', 'change'] }],
  directory: selected.value ? [{ required: true, whitespace: true, message: '请填写网页目录', trigger: ['blur', 'change'] }] : [],
}))
const saving = ref(false)
function edit(item?: Site) {
  selected.value = item ?? null; editorError.value = ''
  Object.assign(form, { name: item?.name ?? '', directory: item?.directory ?? '', spa: item?.spa ?? false, domain: '', auto_certificate: false, accountId: '' })
  formRef.value?.clearValidate()
  editor.value = true
}
async function save() {
  if (saving.value || (requestsCertificate.value && !form.accountId)) return
  const valid = await formRef.value?.validate().catch(() => false)
  if (!valid || saving.value) return
  saving.value = true; editorError.value = ''
  const value = { name: form.name.trim(), directory: form.directory.trim(), spa: form.spa }
  try {
    if (selected.value) {
      await sites.update(selected.value.id, value)
      editor.value = false
      ElMessage.success('网站已保存')
      await load()
      return
    }
    const domain = form.domain.trim()
    const result = await sites.create({
      ...value,
      binding: domain ? { domain, auto_certificate: form.auto_certificate, acme_account_id: requestsCertificate.value ? form.accountId : undefined } : undefined,
    })
    editor.value = false
    if (result.certificate_error) {
      ElMessage.warning(`网站已创建，可通过 HTTP 访问。${result.certificate_error}`)
    } else {
      ElMessage.success(result.binding ? '网站已创建，域名已绑定' : '网站已创建')
    }
    await load(result.operation ? result.binding : undefined)
    if (result.operation && result.binding) void bound({ operation: result.operation, domain: result.binding.domain })
  } catch (e) {
    editorError.value = errorText(e, '保存失败')
  } finally {
    saving.value = false
  }
}
const editorTitle = computed(() => selected.value ? `编辑 ${selected.value.name}` : '新建静态网站')
// 绑定域名：开启自动证书时跟踪申请结果
const binding = ref(false)
const preset = ref<BindPreset | null>(null)
function bind(item: Site) { preset.value = { kind: 'site', id: item.id, name: item.name }; binding.value = true }
async function bound(result: Pick<BindResult, 'operation' | 'domain'>) {
  if (!result.operation) { ElMessage.success('域名已绑定'); await load(); return }
  if (await track(result.operation, `为 ${result.domain} 申请证书`, `${result.domain} 证书已启用`)) await load()
}
</script>
<template>
  <PageHeader title="静态网站" description="直接托管服务器目录中的网页文件。">
    <el-button :disabled="busy || loading" @click="reload"><AppIcon name="refresh"/>刷新</el-button>
    <el-button type="primary" :disabled="busy" @click="edit()"><AppIcon name="plus"/>新建网站</el-button>
  </PageHeader>
  <OperationBanner :operation="operation" :label="operationLabel" @close="operation = null"/>
  <RouterLink v-if="operation?.status === 'failed'" to="/domains">前往域名与证书重试</RouterLink>
  <div class="panel">
    <ListState :loading="loading" :error="loadError" :empty="!items.length" icon="sites" title="还没有静态网站" description="创建网站后，把网页文件放到网站目录。" @retry="reload">
      <template #empty-action><el-button type="primary" :disabled="busy" @click="edit()"><AppIcon name="plus"/>新建网站</el-button></template>
      <div class="tbl-wrap">
        <table class="rtable">
          <thead><tr><th>网站</th><th>目录</th><th>类型</th><th><span class="sr">操作</span></th></tr></thead>
          <tbody>
            <tr v-for="item in items" :key="item.id">
              <td class="first">
                <span class="name">{{ item.name }}</span>
                <span v-for="b in domainsOf(item.id)" :key="b.id" class="sub">
                  <a :href="`${b.certificate_status === 'enabled' ? 'https' : 'http'}://${b.domain}`" target="_blank" rel="noopener">{{ b.domain }}</a>
                  <span v-if="b.certificate_status === 'enabled'"> · HTTPS</span>
                </span>
                <span v-if="!domainsOf(item.id).length" class="sub">未绑定域名</span>
              </td>
              <td data-label="目录" class="mono">{{ item.directory }}</td>
              <td data-label="类型"><span class="tag">{{ item.spa ? '单页应用' : '普通网站' }}</span></td>
              <td class="actions"><span class="row-act"><el-button size="small" :disabled="busy" @click="edit(item)"><AppIcon name="edit"/>编辑</el-button><RowMenu :items="menu(item)" :label="item.name" :disabled="busy"/></span></td>
            </tr>
          </tbody>
        </table>
      </div>
    </ListState>
  </div>
  <el-dialog v-model="editor" :title="editorTitle" width="min(520px, calc(100vw - 32px))" :close-on-click-modal="false" :close-on-press-escape="!saving" :show-close="!saving" append-to-body>
    <el-form id="site-form" ref="formRef" :model="form" :rules="rules" :disabled="saving" :validate-on-rule-change="false" label-position="top" class="dialog-form" scroll-to-error novalidate @submit.prevent="save">
      <el-form-item label="网站名称" prop="name"><el-input v-model="form.name" name="name" maxlength="100" placeholder="例如 公司官网" required/></el-form-item>
      <el-form-item :label="selected ? '网页目录' : '网页目录（可选）'" prop="directory">
        <el-input v-model="form.directory" name="directory" class="mono-input" placeholder="/srv/www/example" spellcheck="false" aria-describedby="site-directory-hint" :required="Boolean(selected)"/>
        <span id="site-directory-hint" class="hint">{{ selected ? '填写服务器上已有目录的绝对路径。' : '留空会自动创建目录，也可填写服务器上已有目录的绝对路径。' }}</span>
      </el-form-item>
      <el-form-item v-if="!selected" label="域名（可选）" prop="domain">
        <el-input v-model="form.domain" name="domain" placeholder="例如 blog.example.com" autocomplete="off" autocapitalize="none" spellcheck="false" aria-describedby="site-domain-hint"/>
        <span id="site-domain-hint" class="hint">请先将域名解析到本服务器。申请证书还需开放公网 80 端口。</span>
      </el-form-item>
      <el-checkbox v-if="!selected && form.domain.trim()" v-model="form.auto_certificate" name="auto_certificate">自动申请 HTTPS 证书</el-checkbox>
      <AcmeAccountSelect v-if="editor && requestsCertificate" v-model="form.accountId"/>
      <el-form-item>
        <el-checkbox v-model="form.spa" name="spa">单页应用（找不到的路径都返回 index.html）</el-checkbox>
      </el-form-item>
      <el-alert v-if="editorError" :title="editorError" type="error" :closable="false" show-icon/>
    </el-form>
    <template #footer><el-button :disabled="saving" @click="editor = false">取消</el-button><el-button type="primary" native-type="submit" form="site-form" :loading="saving" :disabled="requestsCertificate && !form.accountId">{{ selected ? '保存' : '创建' }}</el-button></template>
  </el-dialog>
  <BindDomainDialog v-model:open="binding" :preset="preset" @done="bound"/>
</template>
