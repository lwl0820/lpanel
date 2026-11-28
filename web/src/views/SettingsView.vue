<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import PageHeader from '../components/PageHeader.vue'
import AccessPanel from '../components/AccessPanel.vue'
import MCPPanel from '../components/MCPPanel.vue'
import UpgradePanel from '../components/UpgradePanel.vue'
const route = useRoute()
const router = useRouter()
const tab = computed({
  get: () => route.query.tab === 'upgrade' || route.query.tab === 'mcp' ? route.query.tab : 'access',
  set: (value: string | number) => {
    void router.replace({ query: { ...route.query, tab: value === 'access' ? undefined : String(value) } })
  },
})
</script>
<template>
  <PageHeader title="面板设置" description="管理面板访问、证书账户、MCP 和版本升级。"/>
  <div class="settings">
    <el-tabs v-model="tab" aria-label="面板设置分类">
      <el-tab-pane name="access" label="访问设置" lazy><AccessPanel/></el-tab-pane>
      <el-tab-pane name="mcp" label="MCP 接入" lazy><MCPPanel/></el-tab-pane>
      <el-tab-pane name="upgrade" label="版本升级" lazy><UpgradePanel/></el-tab-pane>
    </el-tabs>
  </div>
</template>
