<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { domains, type Access } from '../domain-api'
import { errorText } from '../format'
import AppIcon from './AppIcon.vue'
const access = ref<Access | null>(null)
const loading = ref(true)
const loadError = ref('')
async function load() {
  loading.value = true; loadError.value = ''
  try { access.value = await domains.access() } catch (e) { loadError.value = errorText(e, '读取访问设置失败') } finally { loading.value = false }
}
onMounted(load)
const url = computed(() => access.value ? `${access.value.https ? 'https' : 'http'}://${access.value.panel_domain || window.location.hostname}:${access.value.panel_port}` : '')
</script>
<template>
  <section class="panel">
    <div class="panel-h"><h2>访问与证书</h2></div>
    <div v-if="loading" class="state" role="status"><span class="spin"></span><span>正在加载…</span></div>
    <div v-else-if="loadError" class="panel-b form" role="alert"><el-alert :title="loadError" type="error" :closable="false" show-icon/><div><el-button @click="load"><AppIcon name="refresh"/>重试</el-button></div></div>
    <div v-else-if="access" class="panel-b form">
      <dl class="kv">
        <dt>访问地址</dt><dd><a :href="url" class="link mono">{{ url }}</a></dd>
        <dt>面板域名</dt><dd><template v-if="access.panel_domain">{{ access.panel_domain }} <span class="muted">· 在“<RouterLink to="/domains" class="link">域名与证书</RouterLink>”中修改</span></template><RouterLink v-else to="/domains" class="link">去绑定面板域名</RouterLink></dd>
        <dt>证书账户</dt><dd><RouterLink to="/domains/acme-accounts" class="link">ACME 账户管理</RouterLink></dd>
      </dl>
    </div>
  </section>
</template>
