<script setup lang="ts">
import type { CertState } from '../cert'
defineProps<{ state: CertState }>()
const pills: Record<CertState, [string, string]> = { valid: ['ok', '有效'], expiring: ['warn', '即将到期'], requesting: ['run', '申请中'], failed: ['bad', '申请失败'], disabled: ['idle', '未启用'] }
</script>
<template><span :class="['pill', pills[state][0]]">{{ pills[state][1] }}</span></template>
