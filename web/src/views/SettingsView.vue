<script setup lang="ts">
import PageHeader from '../components/PageHeader.vue'
import AccessPanel from '../components/AccessPanel.vue'
import MCPPanel from '../components/MCPPanel.vue'
import UpgradePanel from '../components/UpgradePanel.vue'
</script>
<template>
  <PageHeader title="面板设置" description="管理面板访问、证书账户、MCP 和版本升级。"/>
  <div class="settings"><AccessPanel/><MCPPanel/><UpgradePanel/></div>
</template>
