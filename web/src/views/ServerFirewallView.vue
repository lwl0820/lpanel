<script setup lang="ts">
import { computed, onMounted, reactive, ref, shallowRef } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { serverFirewall, type FirewallRule, type FirewallRuleInput, type FirewallStatus } from '../server-firewall-api'
import { errorText } from '../format'
import AppIcon from '../components/AppIcon.vue'
import ListState from '../components/ListState.vue'
import PageHeader from '../components/PageHeader.vue'

const status = shallowRef<FirewallStatus>()
const loading = ref(true)
const loadError = ref('')
const actionError = ref('')
const pending = ref<'toggle' | 'add' | 'remove'>()
const busy = computed(() => loading.value || !!pending.value)
const draft = reactive<Omit<FirewallRuleInput, 'port'> & { port: string }>({ port: '', protocol: 'tcp', action: 'allow' })
const actions: Record<string, string> = { allow: '允许', deny: '拒绝', reject: '拒绝并通知来源' }

function ruleLabel(rule: FirewallRule) {
  return [rule.description, rule.protocol?.toUpperCase(), rule.action && actions[rule.action], rule.source && `来源 ${rule.source}`].filter(Boolean).join(' · ')
}

async function load() {
  loading.value = true
  loadError.value = ''
  try {
    status.value = await serverFirewall.status()
  } catch (e) {
    status.value = undefined
    loadError.value = errorText(e, '无法读取防火墙状态')
  } finally {
    loading.value = false
  }
}
onMounted(load)

async function change(action: NonNullable<typeof pending.value>, send: () => Promise<{ ok: true }>, done: string) {
  if (busy.value || !status.value?.available) return
  pending.value = action
  actionError.value = ''
  try {
    await send()
    if (action === 'add') draft.port = ''
  } catch (e) {
    actionError.value = errorText(e, '操作失败，请重试')
  }
  await load()
  pending.value = undefined
  if (!actionError.value && !loadError.value) ElMessage.success(done)
}

function toggle() {
  const enabled = !status.value?.enabled
  return change('toggle', () => serverFirewall.setEnabled(enabled), enabled ? '防火墙已启用' : '防火墙已停用')
}

function addRule() {
  const port = Number(draft.port)
  if (!Number.isInteger(port) || port < 1 || port > 65535) {
    actionError.value = '请输入 1 到 65535 之间的端口号'
    return
  }
  return change('add', () => serverFirewall.addRule({ ...draft, port }), '规则已添加')
}

async function removeRule(rule: FirewallRule) {
  try {
    await ElMessageBox.confirm(`确定删除规则「${ruleLabel(rule)}」？`, '删除规则', {
      type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消', confirmButtonClass: 'el-button--danger',
    })
  } catch { return }
  await change('remove', () => serverFirewall.removeRule(rule.id), '规则已删除')
}
</script>

<template>
  <PageHeader title="防火墙" description="">
    <el-button :loading="loading && !pending" :disabled="busy" @click="load"><AppIcon v-if="!loading" name="refresh"/>刷新</el-button>
  </PageHeader>

  <div v-if="!status" class="panel">
    <ListState :loading="loading" :error="loadError" @retry="load"/>
  </div>
  <section v-else class="panel">
    <div class="panel-h firewall-heading">
      <h2>防火墙状态</h2>
      <el-button v-if="status.available" :type="status.enabled ? 'default' : 'primary'" :loading="pending === 'toggle'" :disabled="busy" @click="toggle">
        <AppIcon v-if="pending !== 'toggle'" :name="status.enabled ? 'stop' : 'play'"/>{{ status.enabled ? '停用防火墙' : '启用防火墙' }}
      </el-button>
    </div>
    <dl class="panel-b kv">
      <dt>当前状态</dt>
      <dd><span class="pill" :class="status.available && status.enabled ? 'ok' : 'idle'">{{ !status.available ? '不可用' : status.enabled ? '已启用' : '未启用' }}</span></dd>
      <template v-if="status.panel_port">
        <dt>面板端口</dt><dd class="num">{{ status.panel_port }} / TCP</dd>
      </template>
    </dl>
    <div v-if="status.message || !status.available" class="panel-f firewall-note">
      <p class="muted">{{ status.message || '暂时无法管理本机防火墙' }}</p>
    </div>
  </section>

  <el-alert v-if="actionError" :title="actionError" type="error" :closable="false" show-icon/>

  <section v-if="status && (status.available || status.rules.length)" class="panel firewall-rules">
    <div class="panel-h firewall-heading">
      <h2 id="firewall-rules-heading">入站规则</h2>
      <span v-if="status.available && !status.enabled" class="muted">待生效（防火墙未启用）</span>
    </div>
    <el-form v-if="status.available && !status.message" :model="draft" :disabled="busy" label-position="top" class="panel-b firewall-form" novalidate @submit.prevent="addRule">
      <el-form-item label="端口" for="firewall-port">
        <el-input id="firewall-port" v-model="draft.port" type="number" min="1" max="65535" step="1" required/>
      </el-form-item>
      <el-form-item label="协议" for="firewall-protocol">
        <el-select id="firewall-protocol" v-model="draft.protocol">
          <el-option label="TCP" value="tcp"/><el-option label="UDP" value="udp"/>
        </el-select>
      </el-form-item>
      <el-form-item label="处理方式" for="firewall-action">
        <el-select id="firewall-action" v-model="draft.action">
          <el-option label="允许" value="allow"/><el-option label="拒绝" value="deny"/>
        </el-select>
      </el-form-item>
      <el-button type="primary" native-type="submit" :loading="pending === 'add'" :disabled="busy"><AppIcon v-if="pending !== 'add'" name="plus"/>添加规则</el-button>
    </el-form>
    <ListState :loading="false" error="" :empty="!status.rules.length" icon="cert" :title="status.message ? '无法确认入站规则' : '暂无入站规则'">
      <div class="tbl-wrap firewall-table-region" tabindex="0" role="region" aria-labelledby="firewall-rules-heading" :aria-busy="busy">
        <table class="firewall-table" role="table">
          <caption class="sr">本机防火墙入站规则</caption>
          <thead role="rowgroup"><tr role="row">
            <th scope="col" role="columnheader">规则</th><th scope="col" role="columnheader">端口 / 协议</th>
            <th scope="col" role="columnheader">来源</th><th scope="col" role="columnheader">处理方式</th><th scope="col" role="columnheader">操作</th>
          </tr></thead>
          <tbody role="rowgroup">
            <tr v-for="rule in status.rules" :key="rule.id" role="row">
              <th class="firewall-rule-name" scope="row" role="rowheader">{{ rule.description }}</th>
              <td role="cell"><span class="firewall-cell-label" aria-hidden="true">端口 / 协议</span>{{ rule.port ? `${rule.port}${rule.protocol ? ` / ${rule.protocol.toUpperCase()}` : ''}` : rule.protocol?.toUpperCase() || '—' }}</td>
              <td role="cell"><span class="firewall-cell-label" aria-hidden="true">来源</span>{{ rule.source || '—' }}</td>
              <td role="cell"><span class="firewall-cell-label" aria-hidden="true">处理方式</span>{{ actions[rule.action || ''] || '—' }}</td>
              <td class="actions" role="cell">
                <el-button v-if="status.available && rule.removable" type="danger" plain size="small" :disabled="busy" :aria-label="`删除规则：${ruleLabel(rule)}`" @click="removeRule(rule)">删除</el-button>
                <span v-else class="muted">仅可查看</span>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </ListState>
  </section>
</template>
