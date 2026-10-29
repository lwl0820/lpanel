<script setup lang="ts">
import { ref } from 'vue'
import AppIcon from './AppIcon.vue'
defineProps<{ error: string; submitting: boolean }>()
const emit = defineEmits<{ submit: [username: string, password: string] }>()
const username = ref('')
const password = ref('')
// 登录成功后由 App 卸载本组件，密码随之清除
const submit = () => emit('submit', username.value.trim(), password.value)
</script>
<template>
  <main class="login">
    <el-form class="login-card" label-position="top" @submit.prevent="submit">
      <div class="brand"><AppIcon name="logo"/>LPanel</div>
      <div><h1>登录服务器面板</h1><p class="sub">使用初始化时设置的管理员账号。</p></div>
      <el-alert v-if="error" :title="error" type="error" :closable="false" show-icon/>
      <div>
        <el-form-item label="用户名"><el-input v-model="username" autocomplete="username" placeholder="请输入用户名"/></el-form-item>
        <el-form-item label="密码"><el-input v-model="password" type="password" show-password autocomplete="current-password" placeholder="请输入密码"/></el-form-item>
      </div>
      <el-button native-type="submit" type="primary" size="large" :loading="submitting" class="login-submit">登录</el-button>
      <p class="login-foot">LPanel · 服务器管理面板</p>
    </el-form>
  </main>
</template>
