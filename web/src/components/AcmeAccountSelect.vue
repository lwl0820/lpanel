<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { acme, type ACMEAccountList } from '../acme-api'
import { errorText } from '../format'

const props = defineProps<{ preferredId?: string }>()
const accountId = defineModel<string>({ required: true })
const result = ref<ACMEAccountList | null>(null)
const loading = ref(true)
const error = ref('')
const selected = computed(() => result.value?.accounts.find(account => account.id === accountId.value))

async function load() {
  loading.value = true
  error.value = ''
  try {
    const data = await acme.list()
    result.value = data
    const preferred = data.accounts.find(account => account.id === props.preferredId)
    accountId.value = preferred?.id ?? data.default_account_id ?? ''
  } catch (e) {
    error.value = errorText(e, '读取证书账户失败')
  } finally {
    loading.value = false
  }
}
onMounted(load)
</script>

<template>
  <el-form-item v-if="result?.accounts.length" label="ACME 账户">
    <el-select v-model="accountId" name="acme_account_id">
      <el-option v-for="account in result.accounts" :key="account.id" :value="account.id" :label="`${account.name} · ${account.provider_name}${account.id === result.default_account_id ? '（默认）' : ''}`"/>
    </el-select>
    <span v-if="selected && (!selected.agree_tos || selected.needs_eab)" class="hint">请先到 <RouterLink class="link" to="/domains/acme-accounts">ACME 账户管理</RouterLink> 完成此账户的配置。</span>
  </el-form-item>
  <p v-else-if="error" class="hint" role="alert">{{ error }} <el-button link type="primary" @click="load">重试</el-button></p>
  <p v-else-if="loading" class="hint" role="status">正在读取证书账户…</p>
  <p v-else class="hint">申请证书前，请先 <RouterLink class="link" to="/domains/acme-accounts">添加 ACME 账户</RouterLink>。</p>
</template>
