<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { projects, type Project, type ProjectContainer, type ProjectPort } from './projects-api'
import type { Operation } from './api'
import BindingFields from './BindingFields.vue'
const items = ref<Project[]>([])
const error = ref('')
const busy = ref(false)
const editor = ref(false)
const selected = ref('')
const form = reactive({ name: '', compose: '', environment: '' })
const ports = ref<ProjectPort[]>([])
const inspection = ref(false)
const containers = ref<ProjectContainer[]>([])
const log = ref('')
const bindingDialog = ref(false)
const target = ref('')
const domain = reactive({ domain: '', scheme: 'http', auto_certificate: false })
const operation = ref<Operation | null>(null)
let timer: ReturnType<typeof setTimeout> | undefined
const options = computed(() => ports.value.filter(p => p.source === 'ports' && p.protocol === 'tcp'))
const fail = (e: unknown) => { error.value = e instanceof Error ? e.message : '操作失败'; ElMessage.error(error.value) }
async function load() { try { items.value = await projects.list() } catch (e) { fail(e) } }
onMounted(load)
onUnmounted(() => clearTimeout(timer))
async function edit(item?: Project) {
 error.value = ''; ports.value = []; selected.value = item?.id ?? ''
 try { const data = item ? await projects.get(item.id) : undefined; form.name = data?.name ?? ''; form.compose = data?.compose ?? ''; form.environment = data?.environment ?? ''; editor.value = true } catch (e) { fail(e) }
}
async function scan() { busy.value = true; error.value = ''; try { ports.value = await projects.scan({ ...form, id: selected.value }) } catch (e) { fail(e) } finally { busy.value = false } }
async function save() { busy.value = true; error.value = ''; try { await projects.save(selected.value, form); editor.value = false; await load(); ElMessage.success('配置已保存') } catch (e) { fail(e) } finally { busy.value = false } }
async function watchOperation(op: Operation) {
 operation.value = op; busy.value = true
 try { const state = await projects.operation(op.id); operation.value = state; if (state.status === 'running' || state.status === 'queued') { timer = setTimeout(() => void watchOperation(state), 1500); return } busy.value = false; if (state.status === 'failed') { error.value = state.error; ElMessage.error(state.error || '操作失败') } else { ElMessage.success('操作完成') } await load() } catch (e) { busy.value = false; fail(e) }
}
async function action(item: Project, name: string) {
 if (name === 'delete') { try { await ElMessageBox.confirm('删除项目将移除容器和域名绑定，数据卷及挂载目录会保留。', '删除项目', { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消' }) } catch { return } }
 busy.value = true; error.value = ''; try { await watchOperation(await projects.action(item.id, name)) } catch (e) { busy.value = false; fail(e) }
}
async function inspect(item: Project) { selected.value = item.id; inspection.value = true; containers.value = []; log.value = ''; error.value = ''; try { const [state, logs] = await Promise.all([projects.status(item.id), projects.logs(item.id)]); containers.value = state; log.value = logs.log } catch (e) { fail(e) } }
async function bind(item: Project) {
 busy.value = true; error.value = ''; selected.value = item.id; target.value = ''; domain.domain = ''; domain.scheme = 'http'; domain.auto_certificate = false
 try { const input = await projects.get(item.id); ports.value = await projects.scan({ id: input.id, name: input.name, compose: input.compose ?? '', environment: input.environment ?? '' }); bindingDialog.value = true } catch (e) { fail(e) } finally { busy.value = false }
}
async function saveBinding() {
 const port = options.value[Number(target.value)]; if (target.value === '' || !port) { error.value = '请选择应用端口'; return }
 busy.value = true; error.value = ''; try { const result = await projects.bind(selected.value, { ...domain, service: port.service, container_port: port.container_port }); bindingDialog.value = false; if (result.operation) await watchOperation(result.operation); else { busy.value = false; ElMessage.success('域名已绑定') } } catch (e) { busy.value = false; fail(e) }
}
</script>
<template>
 <section class="content-card projects-panel">
  <div class="project-toolbar"><div><h3>Compose 项目</h3><span class="project-muted">部署和管理服务器上的容器应用</span></div><div><el-button :disabled="busy" @click="load">刷新</el-button><el-button type="primary" :disabled="busy" @click="edit()">新建项目</el-button></div></div>
  <el-alert v-if="error" :title="error" type="error" :closable="false" show-icon />
  <el-table :data="items" empty-text="暂无项目" class="project-table"><el-table-column prop="name" label="项目名称" min-width="150"/><el-table-column prop="updated_at" label="配置更新时间" min-width="185"/><el-table-column label="操作" min-width="420"><template #default="{row}"><el-button link type="primary" :disabled="busy" @click="edit(row)">编辑</el-button><el-button link type="primary" :disabled="busy" @click="action(row,'deploy')">部署</el-button><el-button link :disabled="busy" @click="action(row,'start')">启动</el-button><el-button link :disabled="busy" @click="action(row,'stop')">停止</el-button><el-button link :disabled="busy" @click="action(row,'restart')">重启</el-button><el-button link :disabled="busy" @click="inspect(row)">状态与日志</el-button><el-button link type="primary" :disabled="busy" @click="bind(row)">绑定域名</el-button><el-button link type="danger" :disabled="busy" @click="action(row,'delete')">删除</el-button></template></el-table-column></el-table>
  <div v-if="operation" class="operation-box"><strong>最近操作：{{ operation.status === 'success' ? '已完成' : operation.status === 'failed' ? '失败' : '进行中' }}</strong><pre v-if="operation.log || operation.error">{{ operation.error || operation.log }}</pre></div>
 </section>
 <el-dialog v-model="editor" :title="selected ? '编辑项目' : '新建项目'" width="850px" :close-on-click-modal="false"><el-form label-position="top"><el-form-item label="项目名称"><el-input v-model="form.name" maxlength="100" /></el-form-item><el-form-item label="Compose 配置"><el-input v-model="form.compose" type="textarea" :rows="14" placeholder="粘贴 Compose YAML 配置" class="code-input" /></el-form-item><el-form-item label="环境变量（.env）"><el-input v-model="form.environment" type="textarea" :rows="4" placeholder="每行填写 KEY=VALUE" class="code-input" /></el-form-item></el-form><el-alert v-if="error" :title="error" type="error" :closable="false"/><el-table v-if="ports.length" :data="ports"><el-table-column prop="service" label="服务"/><el-table-column prop="container_port" label="容器端口"/><el-table-column label="宿主机端口"><template #default="{row}">{{ row.source === 'expose' ? '未发布（仅内部访问）' : row.published && row.published !== '0' ? row.published : '部署后自动分配' }}</template></el-table-column><el-table-column prop="protocol" label="协议"/></el-table><template #footer><el-button :disabled="busy" @click="scan">扫描端口</el-button><el-button @click="editor=false">取消</el-button><el-button type="primary" :loading="busy" @click="save">保存配置</el-button></template></el-dialog>
 <el-dialog v-model="bindingDialog" title="绑定应用域名" width="560px"><el-form label-position="top"><el-form-item label="服务与容器端口"><el-select v-model="target" placeholder="选择已发布的 TCP 端口" style="width:100%"><el-option v-for="(port,index) in options" :key="index" :value="String(index)" :label="`${port.service} · ${port.container_port} → ${port.published || '自动分配'}`"/></el-select></el-form-item><el-form-item label="上游协议"><el-radio-group v-model="domain.scheme"><el-radio-button value="http">HTTP</el-radio-button><el-radio-button value="https">HTTPS</el-radio-button></el-radio-group></el-form-item><BindingFields v-model:domain="domain.domain" v-model:auto-certificate="domain.auto_certificate"/></el-form><el-alert v-if="!options.length" title="没有可绑定的已发布 TCP 端口，请在 Compose 中添加 ports 映射。" type="info" :closable="false"/><el-alert v-if="error" :title="error" type="error" :closable="false"/><template #footer><el-button @click="bindingDialog=false">取消</el-button><el-button type="primary" :loading="busy" @click="saveBinding">绑定</el-button></template></el-dialog>
 <el-dialog v-model="inspection" title="状态与日志" width="850px"><el-alert v-if="error" :title="error" type="error" :closable="false"/><el-table :data="containers" empty-text="暂无运行记录"><el-table-column prop="Service" label="服务"/><el-table-column prop="Name" label="容器"/><el-table-column prop="State" label="状态"/><el-table-column prop="Health" label="健康检查"/></el-table><pre class="project-logs">{{ log || '暂无日志' }}</pre></el-dialog>
</template>
<style scoped>
.project-toolbar{display:flex;justify-content:space-between;gap:18px;align-items:center;margin-bottom:24px}.project-toolbar h3{margin:0 0 8px}.project-muted{color:#8995a8;font-size:12px}.project-table{margin-top:18px}.operation-box{margin-top:20px;padding:16px;background:#f5f7fb;border-radius:8px;font-size:12px}.operation-box pre,.project-logs{white-space:pre-wrap;overflow-wrap:anywhere;max-height:380px;overflow:auto;font-size:12px;line-height:1.8}.project-logs{background:#152238;color:#dce5f4;padding:18px;border-radius:8px}.code-input :deep(textarea){font-family:ui-monospace,SFMono-Regular,monospace;font-size:12px;line-height:1.7}@media(max-width:600px){.project-toolbar{align-items:flex-start;flex-direction:column}}
</style>
