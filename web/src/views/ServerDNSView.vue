<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, shallowRef } from 'vue'
import type { InputInstance } from 'element-plus'
import { serverDNS, type ServerDNSStatus } from '../server-dns-api'
import { errorText } from '../format'
import ListState from '../components/ListState.vue'
import PageHeader from '../components/PageHeader.vue'

const status = shallowRef<ServerDNSStatus>()
const input = ref<InputInstance>()
const draft = ref('')
const validated = ref(false)
const loading = ref(true)
const action = ref<'save' | 'restore'>()
const loadError = ref('')
const actionError = ref('')
const result = ref('')
const lifetime = new AbortController()
const { signal } = lifetime

const lines = computed(() => draft.value.split('\n').map(line => line.trim()))
const servers = computed(() => lines.value.filter(Boolean))
const fieldError = computed(() => {
  if (!validated.value) return ''
  if (!servers.value.length) return '请至少填写一个 DNS 地址。'
  const maxServers = status.value?.max_servers
  if (maxServers !== undefined && servers.value.length > maxServers) {
    return `最多填写 ${maxServers} 个 DNS 地址。`
  }

  const invalid = lines.value.findIndex(address => address && !isIPAddress(address))
  return invalid < 0 ? '' : `第 ${invalid + 1} 行有误，请填写有效的 IPv4 或 IPv6 地址。`
})

function isIPAddress(address: string) {
  if (!address.includes(':')) {
    const octets = address.split('.')
    return octets.length === 4 && octets.every(octet => /^(0|[1-9][0-9]{0,2})$/.test(octet) && Number(octet) <= 255)
  }
  // 仅接受裸 IPv6 地址，由浏览器校验压缩写法和内嵌 IPv4 地址。
  if (!/^[0-9a-f:.]+$/i.test(address)) return false
  try {
    return Boolean(new window.URL(`http://[${address}]/`).hostname)
  } catch {
    return false
  }
}

async function load(resetDraft = !status.value) {
  loading.value = true
  loadError.value = ''
  try {
    const current = await serverDNS.status(signal)
    if (signal.aborted) return
    status.value = current
    if (resetDraft) draft.value = current.servers.join('\n')
  } catch (error) {
    if (!signal.aborted) loadError.value = errorText(error, '无法读取当前 DNS 设置')
  } finally {
    loading.value = false
  }
}

async function change(kind: 'save' | 'restore') {
  if (loading.value || action.value || !status.value) return
  if (kind === 'save') {
    if (!status.value.can_edit || loadError.value) return
    validated.value = true
    if (fieldError.value) {
      await nextTick()
      input.value?.focus()
      return
    }
  } else if (!status.value.can_restore) {
    return
  }

  action.value = kind
  actionError.value = ''
  result.value = ''
  try {
    const current = kind === 'save'
      ? await serverDNS.save(servers.value, signal)
      : await serverDNS.restore(signal)
    if (signal.aborted) return
    status.value = current
    draft.value = current.servers.join('\n')
    validated.value = false
    result.value = kind === 'save' ? 'DNS 设置已保存。' : '已恢复修改前的 DNS 设置。'
  } catch (error) {
    if (!signal.aborted) {
      actionError.value = errorText(error, kind === 'save' ? '保存 DNS 设置失败' : '恢复 DNS 设置失败')
      await load(false)
    }
  } finally {
    action.value = undefined
  }
}

onMounted(() => { void load() })
onBeforeUnmount(() => lifetime.abort())
</script>

<template>
  <PageHeader title="DNS 设置" description="查看和修改服务器默认使用的 DNS 地址。"/>

  <ListState :loading="loading && !status" :error="status ? '' : loadError" @retry="load()">
    <template v-if="status">
      <section class="panel" aria-labelledby="server-dns-current">
        <div class="panel-h"><h2 id="server-dns-current">当前使用的 DNS</h2></div>
        <div class="panel-b server-dns-settings" :aria-busy="loading">
          <ul v-if="status.servers.length" class="server-dns-addresses">
            <li v-for="(address, index) in status.servers" :key="index">{{ address }}</li>
          </ul>
          <p v-else class="muted">未读到当前使用的 DNS 地址。</p>
          <p v-if="status.message || !status.can_edit" class="muted">
            {{ status.message || '当前服务器的 DNS 设置无法通过面板修改。' }}
          </p>
        </div>
      </section>

      <section v-if="status.can_edit || status.can_restore" class="panel" aria-labelledby="server-dns-edit">
        <div class="panel-h"><h2 id="server-dns-edit">默认 DNS 设置</h2></div>
        <div class="panel-b server-dns-settings">
          <el-form v-if="status.can_edit" label-position="top" @submit.prevent="change('save')">
            <el-form-item label="待保存的 DNS 地址" for="server-dns-addresses" :validate-status="fieldError ? 'error' : undefined">
              <el-input id="server-dns-addresses" ref="input" v-model="draft" type="textarea" :rows="3"
                spellcheck="false" autocapitalize="off" :disabled="!!action || loading || !!loadError"
                :aria-invalid="!!fieldError" :aria-describedby="fieldError ? 'server-dns-help server-dns-error' : 'server-dns-help'"/>
            </el-form-item>
            <div>
              <p id="server-dns-help" class="muted">
                每行填写一个 IPv4 或 IPv6 地址{{ status.max_servers === undefined ? '。' : `，最多 ${status.max_servers} 个。` }}
              </p>
              <div role="status" class="server-dns-field-error">
                <p v-if="fieldError" id="server-dns-error">{{ fieldError }}</p>
              </div>
            </div>
            <el-button type="primary" native-type="submit" :loading="action === 'save'"
              :disabled="!!action || loading || !!loadError">保存设置</el-button>
          </el-form>
          <div v-if="status.can_restore" class="server-dns-settings">
            <p class="muted">可恢复通过面板首次修改前的 DNS 设置。</p>
            <div>
              <el-button :loading="action === 'restore'" :disabled="!!action || loading"
                @click="change('restore')">恢复原设置</el-button>
            </div>
          </div>
        </div>
      </section>

      <el-alert v-if="actionError" :title="actionError" role="alert" type="error" :closable="false" show-icon/>
      <div v-if="loadError" class="server-dns-settings">
        <el-alert :title="loadError" role="alert" type="error" :closable="false" show-icon/>
        <div><el-button :disabled="!!action || loading" @click="load()">重新读取</el-button></div>
      </div>
      <div role="status"><p v-if="result" class="banner ok">{{ result }}</p></div>
    </template>
  </ListState>
</template>
