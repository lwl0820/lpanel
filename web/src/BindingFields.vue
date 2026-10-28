<script setup lang="ts">
defineProps<{ domain: string; autoCertificate: boolean }>()
defineEmits<{ 'update:domain':[value:string]; 'update:autoCertificate':[value:boolean] }>()
</script>
<template><el-form-item label="绑定域名"><div class="domain-fields"><el-input :model-value="domain" placeholder="例如 app.example.com" @update:model-value="$emit('update:domain',$event)"/><el-checkbox :model-value="autoCertificate" @update:model-value="$emit('update:autoCertificate',Boolean($event))">自动申请证书</el-checkbox></div></el-form-item></template>
<style scoped>.domain-fields{display:flex;gap:16px;align-items:center;width:100%;flex-wrap:wrap}.domain-fields .el-input{flex:1;min-width:180px}</style>
