<script setup lang="ts">
import { computed, nextTick, onMounted, ref, useId, useTemplateRef } from 'vue'
import { ElMessage } from 'element-plus'
import { mcp, type MCPSettings, type MCPToken } from '../mcp-api'
import { errorText } from '../format'
import ListState from './ListState.vue'

const settings = ref<MCPSettings>()
const loading = ref(true)
const loadError = ref('')
const actionError = ref('')
const pending = ref<'' | 'toggle' | 'create' | 'revoke'>('')
const name = ref('')
const issuedToken = ref('')
const revokeTarget = ref<MCPToken>()
const revokeDialog = useTemplateRef('revokeDialog')
const switchId = useId()
const nameHintId = useId()
const revokeTitleId = useId()
const endpoint = computed(() => settings.value ? new window.URL(settings.value.path, window.location.origin).href : '')

async function load() {
  loading.value = true
  loadError.value = ''
  try {
    settings.value = await mcp.settings()
  } catch (e) {
    loadError.value = errorText(e, '读取 MCP 设置失败')
  } finally {
    loading.value = false
  }
}
onMounted(load)

async function setEnabled(value: string | number | boolean) {
  pending.value = 'toggle'
  actionError.value = ''
  try {
    settings.value = await mcp.setEnabled(value === true)
  } catch (e) {
    actionError.value = errorText(e, '保存 MCP 设置失败')
  } finally {
    pending.value = ''
  }
}

async function createToken() {
  const tokenName = name.value.trim()
  if (!tokenName || pending.value || issuedToken.value) return
  pending.value = 'create'
  actionError.value = ''
  try {
    const result = await mcp.createToken(tokenName)
    issuedToken.value = result.token
    name.value = ''
    await load()
  } catch (e) {
    actionError.value = errorText(e, '签发令牌失败')
  } finally {
    pending.value = ''
  }
}

async function confirmRevoke(token: MCPToken) {
  revokeTarget.value = token
  await nextTick()
  revokeDialog.value?.showModal()
}

async function revokeToken() {
  if (!revokeTarget.value || pending.value) return
  revokeDialog.value?.close()
  pending.value = 'revoke'
  actionError.value = ''
  try {
    await mcp.revokeToken(revokeTarget.value.id)
    await load()
    ElMessage.success('令牌已撤销')
  } catch (e) {
    actionError.value = errorText(e, '撤销令牌失败')
  } finally {
    pending.value = ''
  }
}

async function copyText(value: string, label: string) {
  try {
    await window.navigator.clipboard.writeText(value)
    ElMessage.success(`${label}已复制`)
  } catch {
    ElMessage.warning(`请手动复制${label}`)
  }
}
</script>

<template>
  <section class="panel">
    <div class="panel-h"><h2>MCP 接入</h2></div>
    <div v-if="issuedToken" class="panel-b form">
      <el-alert title="令牌仅显示这一次，请复制并妥善保管。" type="warning" :closable="false" show-icon/>
      <dl class="kv">
        <dt>新令牌</dt><dd><code>{{ issuedToken }}</code></dd>
      </dl>
      <div>
        <el-button type="primary" @click="copyText(issuedToken, '令牌')">复制令牌</el-button>
        <el-button @click="issuedToken = ''">关闭令牌展示</el-button>
      </div>
    </div>
    <ListState :loading="loading" :error="loadError" @retry="load">
      <div v-if="settings" class="panel-b form">
        <el-alert v-if="actionError" :title="actionError" type="error" :closable="false" show-icon/>
        <div class="field">
          <label :for="switchId">启用 MCP</label>
          <el-switch :id="switchId" :model-value="settings.enabled" :loading="pending === 'toggle'"
            :disabled="!!pending || (!settings.enabled && !settings.tokens.length)" @change="setEnabled"/>
          <span v-if="!settings.tokens.length" class="hint">签发令牌后即可启用 MCP。</span>
        </div>
        <dl class="kv">
          <dt>连接地址</dt>
          <dd><code>{{ endpoint }}</code> <el-button text size="small" @click="copyText(endpoint, '连接地址')">复制连接地址</el-button></dd>
          <dt>传输方式</dt><dd>Streamable HTTP</dd>
          <dt>鉴权请求头</dt><dd><code>Authorization: Bearer &lt;令牌&gt;</code></dd>
        </dl>
        <p class="muted">通过公网连接时，请使用启用了 HTTPS 的面板地址。</p>
        <el-form label-position="top" :disabled="!!pending || !!issuedToken" @submit.prevent="createToken">
          <el-form-item label="令牌名称" required>
            <span :id="nameHintId" class="hint">建议为每个客户端签发单独的令牌。每枚令牌均可使用全部 MCP 工具，并拥有与面板文件管理相同的宿主机访问范围。</span>
            <el-input v-model="name" name="mcp_token_name" placeholder="例如 运维助手" required :aria-describedby="nameHintId"/>
          </el-form-item>
          <el-button native-type="submit" type="primary" :disabled="!name.trim() || !!issuedToken" :loading="pending === 'create'">签发令牌</el-button>
        </el-form>
      </div>
      <table v-if="settings?.tokens.length" class="rtable mcp-tokens">
        <thead><tr><th scope="col">令牌名称</th><th scope="col" class="actions">操作</th></tr></thead>
        <tbody>
          <tr v-for="token in settings.tokens" :key="token.id">
            <td class="first">{{ token.name }}</td>
            <td class="actions">
              <el-button text type="danger" :aria-label="`撤销令牌 ${token.name}`" :disabled="!!pending"
                :loading="pending === 'revoke' && token.id === revokeTarget?.id" @click="confirmRevoke(token)">撤销</el-button>
            </td>
          </tr>
        </tbody>
      </table>
    </ListState>
  </section>
  <dialog ref="revokeDialog" class="panel mcp-revoke-dialog" :aria-labelledby="revokeTitleId">
    <div class="panel-h"><h2 :id="revokeTitleId">撤销令牌</h2></div>
    <div class="panel-b form">
      <p>撤销「{{ revokeTarget?.name }}」后，使用此令牌的客户端将立即失去访问权限。</p>
      <p v-if="settings?.enabled && settings.tokens.length === 1">这是最后一枚令牌，撤销后 MCP 将自动关闭。</p>
    </div>
    <div class="panel-f">
      <el-button autofocus @click="revokeDialog?.close()">取消</el-button>
      <el-button type="danger" @click="revokeToken">撤销令牌</el-button>
    </div>
  </dialog>
</template>
