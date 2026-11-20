import { createApp } from 'vue'
import ElementPlus from 'element-plus'
import zhCn from 'element-plus/es/locale/lang/zh-cn'
import 'element-plus/dist/index.css'
import App from './App.vue'
import { createAppRouter } from './router'
import './style.css'
const loginPath = document.querySelector<HTMLMetaElement>('meta[name="lpanel-login-path"]')?.content ?? ''
createApp(App, { loginPath }).use(createAppRouter(undefined, loginPath)).use(ElementPlus, { locale: zhCn }).mount('#app')
