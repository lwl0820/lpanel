<script setup lang="ts">
import PageHeader from '../components/PageHeader.vue'
import AccessPanel from '../components/AccessPanel.vue'
import UpgradePanel from '../components/UpgradePanel.vue'
</script>
<template>
  <PageHeader title="面板设置" description="面板的访问方式、证书账号和版本升级。"/>
  <div class="settings"><AccessPanel/><UpgradePanel/></div>
</template>
