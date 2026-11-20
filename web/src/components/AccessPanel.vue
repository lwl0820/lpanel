<script setup lang="ts">
import { computed, nextTick, onMounted, ref, useId, useTemplateRef } from 'vue'
import { ElMessage } from 'element-plus'
import { APIError } from '../api'
import { domains, panelLoginURL, type Access } from '../domain-api'
import { errorText } from '../format'
import AppIcon from './AppIcon.vue'
const access = ref<Access | null>(null)
const loading = ref(true)
const loadError = ref('')
const loginPath = ref('')
const hintId = useId()
const pathErrorId = useId()
const pathInput = useTemplateRef('pathInput')
const addressLink = useTemplateRef('addressLink')
const saving = ref(false)
const saveError = ref('')
const pathError = ref('')
async function load() {
  loading.value = true; loadError.value = ''
  try {
    access.value = await domains.access()
    loginPath.value = access.value.login_path || ''
  } catch (e) { loadError.value = errorText(e, '读取访问设置失败') } finally { loading.value = false }
}
onMounted(load)
const url = computed(() => access.value ? panelLoginURL(access.value) : '')
async function saveLoginPath() {
  if (saving.value) return
  saving.value = true
  saveError.value = ''
  pathError.value = ''
  try {
    access.value = await domains.setLoginPath(loginPath.value)
    loginPath.value = access.value.login_path
    ElMessage.success('登录地址已保存')
  } catch (e) {
    if (e instanceof APIError && e.code === 'invalid_login_path') pathError.value = e.message
    else saveError.value = errorText(e, '保存登录路径失败')
  } finally { saving.value = false }
  if (pathError.value) {
    await nextTick()
    pathInput.value?.focus()
  }
}
async function copyAddress() {
  try {
    await window.navigator.clipboard.writeText(url.value)
    ElMessage.success('登录地址已复制')
  } catch {
    const selection = window.getSelection()
    if (selection && addressLink.value) {
      const range = document.createRange()
      range.selectNodeContents(addressLink.value)
      selection.removeAllRanges()
      selection.addRange(range)
    }
    ElMessage.warning('无法自动复制，请手动复制上方的登录地址')
  }
}
</script>
<template>
  <section class="panel">
    <div class="panel-h"><h2>访问与证书</h2></div>
    <div v-if="loading" class="state" role="status"><span class="spin"></span><span>正在加载…</span></div>
    <div v-else-if="loadError" class="panel-b form" role="alert"><el-alert :title="loadError" type="error" :closable="false" show-icon/><div><el-button @click="load"><AppIcon name="refresh"/>重试</el-button></div></div>
    <div v-else-if="access" class="panel-b form">
      <dl class="kv">
        <dt>登录地址</dt><dd><a ref="addressLink" :href="url" class="link mono">{{ url }}</a> <el-button text size="small" native-type="button" @click="copyAddress">复制登录地址</el-button></dd>
        <dt>面板域名</dt><dd><template v-if="access.panel_domain">{{ access.panel_domain }} <span class="muted">· 在“<RouterLink to="/domains" class="link">域名与证书</RouterLink>”中修改</span></template><RouterLink v-else to="/domains" class="link">去绑定面板域名</RouterLink></dd>
        <dt>证书账户</dt><dd><RouterLink to="/domains/acme-accounts" class="link">ACME 账户管理</RouterLink></dd>
      </dl>
      <el-form label-position="top" :disabled="saving" @submit.prevent="saveLoginPath">
        <el-form-item label="登录路径" :validate-status="pathError ? 'error' : ''">
          <span :id="hintId" class="hint">例如 /abcdef。留空时直接访问面板地址即可登录。</span>
          <el-input ref="pathInput" v-model="loginPath" name="login_path" placeholder="/abcdef" autocomplete="off" autocapitalize="none" autocorrect="off" :spellcheck="false" :aria-describedby="pathError ? `${hintId} ${pathErrorId}` : hintId" :aria-invalid="!!pathError" @update:model-value="pathError = ''"/>
          <span v-if="pathError" :id="pathErrorId" class="err-line">{{ pathError }}</span>
        </el-form-item>
        <el-form-item v-if="saveError"><el-alert :title="saveError" type="error" :closable="false" show-icon/></el-form-item>
        <el-button native-type="submit" type="primary" :loading="saving">保存登录路径</el-button>
      </el-form>
    </div>
  </section>
</template>
