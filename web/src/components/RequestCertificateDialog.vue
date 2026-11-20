<script setup lang="ts">
import { ref, useId, watch } from 'vue'
import type { Operation } from '../api'
import { certificateActionLabel } from '../cert'
import { domains, type Binding } from '../domain-api'
import { errorText } from '../format'
import AcmeAccountSelect from './AcmeAccountSelect.vue'

export interface CertificateRequestResult {
  binding: Binding
  operation: Operation
  accessURL: string
}

const props = defineProps<{ binding: Binding | null }>()
const open = defineModel<boolean>('open', { required: true })
const emit = defineEmits<{ done: [result: CertificateRequestResult] }>()
const formId = useId()
const accountId = ref('')
const saving = ref(false)
const error = ref('')

watch(open, value => {
  if (!value) return
  accountId.value = ''
  error.value = ''
})

async function submit() {
  const binding = props.binding
  if (!binding || !accountId.value || saving.value) return
  saving.value = true
  error.value = ''
  try {
    const accessURL = binding.kind === 'panel'
      ? `https://${binding.domain}:${(await domains.access()).panel_port}`
      : ''
    const operation = await domains.certificate(binding.id, accountId.value)
    open.value = false
    emit('done', { binding, operation, accessURL })
  } catch (e) {
    error.value = errorText(e, '申请证书失败')
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <el-dialog v-model="open" :title="binding ? certificateActionLabel(binding) : '申请证书'" width="min(520px, calc(100vw - 32px))" :close-on-click-modal="false" :close-on-press-escape="!saving" :show-close="!saving" append-to-body>
    <el-form v-if="open && binding" :id="formId" label-position="top" class="dialog-form" :disabled="saving" @submit.prevent="submit">
      <p class="hint">域名：{{ binding.domain }}</p>
      <AcmeAccountSelect v-model="accountId" :preferred-id="binding.certificate_account_id"/>
      <el-alert v-if="error" :title="error" type="error" :closable="false" show-icon/>
    </el-form>
    <template #footer>
      <el-button :disabled="saving" @click="open = false">取消</el-button>
      <el-button type="primary" native-type="submit" :form="formId" :loading="saving" :disabled="!accountId">{{ binding ? certificateActionLabel(binding) : '申请证书' }}</el-button>
    </template>
  </el-dialog>
</template>
