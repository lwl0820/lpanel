<script setup lang="ts">
import { onMounted, onUnmounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import BindingFields from './BindingFields.vue'
import { sites, type Site } from './sites-api'
import { domains, type Binding } from './domain-api'
import type { Operation } from './api'
const items = ref<Site[]>([])
const bindings = ref<Binding[]>([])
const error = ref('')
const busy = ref(false)
const editor = ref(false)
const bindingDialog = ref(false)
const selected = ref('')
const form = reactive({name: '', directory: '', spa: false})
const domain = reactive({domain: '', auto_certificate: false})
const operation = ref<Operation | null>(null)
let timer: ReturnType<typeof setTimeout> | undefined
const fail = (e: unknown) => {error.value = e instanceof Error ? e.message : '操作失败'; ElMessage.error(error.value)}
async function load() {try { [items.value, bindings.value] = await Promise.all([sites.list(), domains.list()]) } catch (e) {fail(e)} }
onMounted(load)
onUnmounted(() => clearTimeout(timer))
function edit(site?: Site) {selected.value = site?.id ?? ''; form.name = site?.name ?? ''; form.directory = site?.directory ?? ''; form.spa = site?.spa ?? false; error.value = ''; editor.value = true}
async function save() {busy.value = true; error.value = ''; try { await sites.save(selected.value, form); editor.value = false; await load(); ElMessage.success('网站已保存') } catch(e) {fail(e)} finally {busy.value = false} }
async function remove(site: Site) {
 try {await ElMessageBox.confirm('删除网站将解除域名绑定，服务器上的内容目录会保留。', '删除网站', {type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消'})} catch {return}
 busy.value = true; error.value = ''; try {await sites.remove(site.id); await load(); ElMessage.success('网站已删除')} catch(e) {fail(e)} finally {busy.value = false}
}
function bind(site: Site) {selected.value = site.id; domain.domain = ''; domain.auto_certificate = false; error.value = ''; bindingDialog.value = true}
async function watchOperation(op: Operation) {
 operation.value = op; busy.value = true
 try {const current = await domains.operation(op.id); operation.value = current; if (current.status === 'running' || current.status === 'queued') {timer = setTimeout(() => void watchOperation(current), 1500); return} busy.value = false; if (current.status === 'failed') fail(new Error(current.error || '证书申请失败')); else ElMessage.success('证书已启用'); await load()} catch(e) {busy.value = false; fail(e)}
}
async function saveBinding() {busy.value = true; error.value = ''; try {const result = await sites.bind(selected.value, domain); bindingDialog.value = false; if (result.operation) await watchOperation(result.operation); else {busy.value = false; await load(); ElMessage.success('域名已绑定')}} catch(e) {busy.value = false; fail(e)} }
</script>
<template>
 <section class="content-card sites-panel">
  <div class="site-toolbar"><div><h3>静态网站</h3><span class="site-muted">使用服务器上的目录托管网站</span></div><div><el-button :disabled="busy" @click="load">刷新</el-button><el-button type="primary" :disabled="busy" @click="edit()">添加网站</el-button></div></div>
  <el-alert v-if="error" :title="error" type="error" :closable="false" show-icon />
  <el-table :data="items" empty-text="暂无静态网站" class="site-table"><el-table-column prop="name" label="网站名称" min-width="130"/><el-table-column prop="directory" label="服务器目录" min-width="190" show-overflow-tooltip/><el-table-column label="路由回退" width="100"><template #default="{row}">{{ row.spa ? '已开启' : '未开启' }}</template></el-table-column><el-table-column label="绑定域名" min-width="185"><template #default="{row}"><div v-for="item in bindings.filter(b => b.kind === 'site' && b.resource_id === row.id)" :key="item.id" class="site-binding">{{ item.domain }} <el-tag size="small" :type="item.certificate_status === 'enabled' ? 'success' : 'info'">{{ item.certificate_status === 'enabled' ? 'HTTPS' : 'HTTP' }}</el-tag></div><span v-if="!bindings.some(b => b.kind === 'site' && b.resource_id === row.id)" class="site-muted">未绑定</span></template></el-table-column><el-table-column label="操作" min-width="200"><template #default="{row}"><el-button link type="primary" :disabled="busy" @click="edit(row)">编辑</el-button><el-button link type="primary" :disabled="busy" @click="bind(row)">绑定域名</el-button><el-button link type="danger" :disabled="busy" @click="remove(row)">删除</el-button></template></el-table-column></el-table>
  <div v-if="operation" class="site-operation"><strong>证书申请：{{ operation.status === 'success' ? '已完成' : operation.status === 'failed' ? '失败' : '进行中' }}</strong><pre v-if="operation.error">{{ operation.error }}</pre></div>
 </section>
 <el-dialog v-model="editor" :title="selected ? '编辑网站' : '添加网站'" width="560px" :close-on-click-modal="false"><el-form label-position="top"><el-form-item label="网站名称"><el-input v-model="form.name" maxlength="100" placeholder="例如 公司官网"/></el-form-item><el-form-item label="服务器目录"><el-input v-model="form.directory" placeholder="例如 /var/www/example"/></el-form-item><el-form-item label="单页应用"><el-switch v-model="form.spa" active-text="开启路由回退"/></el-form-item></el-form><el-alert v-if="error" :title="error" type="error" :closable="false"/><template #footer><el-button @click="editor=false">取消</el-button><el-button type="primary" :loading="busy" @click="save">保存网站</el-button></template></el-dialog>
 <el-dialog v-model="bindingDialog" title="绑定网站域名" width="560px"><el-form label-position="top"><BindingFields v-model:domain="domain.domain" v-model:auto-certificate="domain.auto_certificate"/></el-form><el-alert v-if="error" :title="error" type="error" :closable="false"/><template #footer><el-button @click="bindingDialog=false">取消</el-button><el-button type="primary" :loading="busy" @click="saveBinding">绑定</el-button></template></el-dialog>
</template>
<style scoped>
.site-toolbar{display:flex;justify-content:space-between;align-items:center;gap:18px;margin-bottom:24px}.site-toolbar h3{margin:0 0 8px}.site-muted{font-size:12px;color:#8995a8}.site-table{margin-top:18px}.site-binding{display:flex;gap:8px;align-items:center;margin:4px 0;font-size:12px}.site-operation{margin-top:20px;padding:16px;border-radius:8px;background:#f5f7fb;font-size:12px}.site-operation pre{white-space:pre-wrap;overflow-wrap:anywhere}@media(max-width:600px){.site-toolbar{align-items:flex-start;flex-direction:column}}
</style>
