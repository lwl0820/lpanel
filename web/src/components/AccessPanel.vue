<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { domains, type Access } from '../domain-api'
import { errorText } from '../format'
import AppIcon from './AppIcon.vue'
// 只编辑联系邮箱和服务条款；acme_server 原样带回
const access = ref<Access | null>(null)
const form = reactive({ acme_email: '', agree_tos: false })
const loading = ref(true)
const loadError = ref('')
const saving = ref(false)
const saveError = ref('')
async function load() {
  loading.value = true; loadError.value = ''
  try { access.value = await domains.access(); Object.assign(form, { acme_email: access.value.acme_email, agree_tos: access.value.agree_tos }) } catch (e) { loadError.value = errorText(e, '读取访问设置失败') } finally { loading.value = false }
}
onMounted(load)
const url = computed(() => access.value ? `${access.value.https ? 'https' : 'http'}://${access.value.panel_domain || window.location.hostname}:${access.value.panel_port}` : '')
async function save() {
  if (!access.value) return
  saving.value = true; saveError.value = ''
  try { access.value = await domains.settings({ acme_email: form.acme_email.trim(), acme_server: access.value.acme_server, agree_tos: form.agree_tos }); ElMessage.success('设置已保存') } catch (e) { saveError.value = errorText(e, '保存失败') } finally { saving.value = false }
}
</script>
<template>
  <form class="panel" novalidate @submit.prevent="save">
    <div class="panel-h"><h2>访问与证书</h2></div>
    <div v-if="loading" class="state" role="status"><span class="spin"></span><span>正在加载…</span></div>
    <div v-else-if="loadError" class="panel-b form" role="alert"><el-alert :title="loadError" type="error" :closable="false" show-icon/><div><el-button @click="load"><AppIcon name="refresh"/>重试</el-button></div></div>
    <template v-else-if="access">
      <div class="panel-b form">
        <dl class="kv">
          <dt>访问地址</dt><dd><a :href="url" class="link mono">{{ url }}</a></dd>
          <dt>面板域名</dt><dd><template v-if="access.panel_domain">{{ access.panel_domain }} <span class="muted">· 在“<RouterLink to="/domains" class="link">域名与证书</RouterLink>”中修改</span></template><RouterLink v-else to="/domains" class="link">去绑定面板域名</RouterLink></dd>
        </dl>
        <el-alert v-if="saveError" :title="saveError" type="error" :closable="false" show-icon/>
        <div class="field"><label for="acme-email">证书联系邮箱（可选）</label><el-input id="acme-email" v-model="form.acme_email" type="email" placeholder="ops@example.com"/><span class="hint">证书即将到期或出现问题时，Let's Encrypt 会发邮件通知。</span></div>
        <div class="field"><el-checkbox v-model="form.agree_tos">我同意 <a class="link" href="https://letsencrypt.org/repository/" target="_blank" rel="noopener" @click.stop>Let's Encrypt 服务条款</a></el-checkbox><span class="hint">申请证书前必须同意。</span></div>
      </div>
      <div class="panel-f"><el-button type="primary" native-type="submit" :loading="saving">保存设置</el-button></div>
    </template>
  </form>
</template>
