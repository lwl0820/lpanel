<script setup lang="ts">
import { icons, type IconName } from '../icons'
defineProps<{ name: IconName | 'logo' }>()
</script>
<template>
  <svg v-if="name === 'logo'" class="icon logo" viewBox="0 0 26 26" aria-hidden="true"><rect width="26" height="26" rx="7" fill="var(--accent)"/><path d="M8 7v12h10" fill="none" stroke="#fff" stroke-width="2.6" stroke-linecap="round" stroke-linejoin="round"/></svg>
  <!-- v-html 只渲染 icons.ts 中的常量 -->
  <svg v-else class="icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" v-html="icons[name]"/>
</template>
