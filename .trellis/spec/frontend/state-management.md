# 页面状态

登录状态由 `App.vue` 持有。页面加载后根据 `auth.me()` 决定显示登录页还是路由内容，退出成功后清空当前用户。会话由后端 Cookie 维持，浏览器状态只保存请求所需的界面数据。 [应用外框](../../../web/src/App.vue) · [请求入口](../../../web/src/api.ts)

列表、弹窗、表单和选中项由所属页面维护。用计算属性从实体推导界面状态，例如静态网站按 `kind` 和 `resource_id` 获取域名，概览从接口返回的证书数据计算待处理事项。 [静态网站](../../../web/src/views/SitesView.vue) · [概览](../../../web/src/views/OverviewView.vue)

列表加载、空态和报错通过 `ListState` 渲染。创建或编辑成功后重新读取服务端列表。证书任务提交后，静态网站先显示创建响应中的 HTTP 绑定，等操作结束再刷新 HTTPS 状态。失败原因由操作条或表单提示展示。 [列表状态](../../../web/src/components/ListState.vue) · [建站提交](../../../web/src/views/SitesView.vue)

面板升级需要跨服务重启恢复轮询。`UpgradePanel` 仅为这条流程用 `sessionStorage` 持久化操作信息。其他页面沿用本地响应式状态。 [升级重连](../../../web/src/components/UpgradePanel.vue)
