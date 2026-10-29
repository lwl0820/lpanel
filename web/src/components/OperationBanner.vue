<script setup lang="ts">
import { ref, watch } from 'vue'
import AppIcon from './AppIcon.vue'
import type { Operation } from '../api'
// label 是动作短语，如“部署 blog”
// queued / running 显示进行中；failed 显示原因和日志；success 不显示（调用方提示成功）
const props = defineProps<{ operation: Operation | null; label: string }>()
defineEmits<{ close: [] }>()
const showLog = ref(false)
watch(() => props.operation?.id, () => { showLog.value = false })
</script>
<template>
  <div v-if="operation && (operation.status === 'queued' || operation.status === 'running')" class="banner run" role="status">
    <span class="spin"></span><span class="txt">{{ operation.status === 'queued' ? `${label} 已提交，排队中…` : `正在${label}…` }}</span>
    <button v-if="operation.log" type="button" class="link" @click="showLog = !showLog">{{ showLog ? '收起日志' : '查看日志' }}</button>
  </div>
  <div v-else-if="operation?.status === 'failed'" class="banner bad" role="alert">
    <AppIcon name="alert"/><span class="txt"><b>{{ label }} 失败</b>{{ operation.error }}</span>
    <button v-if="operation.log" type="button" class="link" @click="showLog = !showLog">{{ showLog ? '收起日志' : '查看日志' }}</button>
    <button type="button" class="icon-btn" aria-label="关闭" title="关闭" @click="$emit('close')"><AppIcon name="x"/></button>
  </div>
  <pre v-if="showLog && operation && operation.status !== 'success' && operation.log" class="log">{{ operation.log }}</pre>
</template>
