<script setup lang="ts">
import AppIcon from './AppIcon.vue'
import type { IconName } from '../icons'
export interface MenuItem { label: string; icon: IconName; danger?: boolean; disabled?: boolean; divided?: boolean; onSelect: () => void }
const props = defineProps<{ items: MenuItem[]; label: string; disabled?: boolean }>()
const select = (index: number) => props.items[index]?.onSelect()
</script>
<template>
  <el-dropdown trigger="click" placement="bottom-end" :disabled="disabled" @command="select">
    <button type="button" class="icon-btn" :disabled="disabled" :aria-label="`${label} 的更多操作`" :title="`${label} 的更多操作`"><AppIcon name="more"/></button>
    <template #dropdown>
      <el-dropdown-menu class="row-menu">
        <el-dropdown-item v-for="(item, index) in items" :key="item.label" :command="index" :disabled="item.disabled" :divided="item.divided" :class="{ danger: item.danger }"><AppIcon :name="item.icon"/>{{ item.label }}</el-dropdown-item>
      </el-dropdown-menu>
    </template>
  </el-dropdown>
</template>
