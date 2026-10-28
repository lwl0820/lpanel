<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { domains } from './domain-api'
const form=reactive({acme_email:'',acme_server:'',agree_tos:false}),error=ref(''),saving=ref(false),url=ref('')
onMounted(async()=>{try{const value=await domains.access();Object.assign(form,{acme_email:value.acme_email,acme_server:value.acme_server,agree_tos:value.agree_tos});url.value=`${value.https?'https':'http'}://${value.panel_domain||window.location.hostname}:${value.panel_port}`}catch(e){error.value=e instanceof Error?e.message:'加载失败'}})
async function save(){saving.value=true;try{await domains.settings(form);ElMessage.success('设置已保存');error.value=''}catch(e){error.value=e instanceof Error?e.message:'保存失败'}finally{saving.value=false}}
</script>
<template><section class="content-card"><h3>访问与证书设置</h3><el-alert v-if="error" :title="error" type="error" :closable="false"/><p>面板访问地址：<a :href="url">{{ url }}</a></p><el-form label-position="top" class="access-form" @submit.prevent="save"><el-form-item label="证书联系邮箱（可选）"><el-input v-model="form.acme_email" placeholder="用于证书联系"/></el-form-item><el-form-item><el-checkbox v-model="form.agree_tos">我同意 <a href="https://letsencrypt.org/repository/" target="_blank" rel="noopener">Let’s Encrypt 服务条款</a></el-checkbox></el-form-item><el-button type="primary" native-type="submit" :loading="saving">保存设置</el-button></el-form></section></template>
<style scoped>.access-form{max-width:500px;margin-top:24px}</style>
