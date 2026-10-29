<script setup lang="ts">
import AppIcon from './AppIcon.vue'
import type { IconName } from '../icons'
// 列表的加载 / 错误 / 空态；有数据时渲染默认插槽。不需要空态时省略 empty 及其文字
defineProps<{ loading: boolean; error: string; empty?: boolean; icon?: IconName; title?: string; description?: string }>()
defineEmits<{ retry: [] }>()
</script>
<template>
  <div v-if="loading" class="state" role="status"><span class="spin"></span><span>正在加载…</span></div>
  <div v-else-if="error" class="empty" role="alert"><div class="ico bad"><AppIcon name="alert"/></div><h3>加载失败</h3><p>{{ error }}</p><el-button @click="$emit('retry')"><AppIcon name="refresh"/>重试</el-button></div>
  <div v-else-if="empty" class="empty"><div v-if="icon" class="ico"><AppIcon :name="icon"/></div><h3>{{ title }}</h3><p>{{ description }}</p><slot name="empty-action"/></div>
  <slot v-else/>
</template>
