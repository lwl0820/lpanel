# 前端类型约定

前端以 `strict` 模式运行 `vue-tsc`。`src/api.ts` 负责 `request<T>()` 和 `APIError`，各模块在 `*-api.ts` 定义请求与响应类型。类型字段与服务端 JSON 一致，界面调用这些接口以获得返回值的类型。 [TypeScript 配置](../../../web/tsconfig.json) · [通用 API](../../../web/src/api.ts)

业务入参用 `Pick` 或独立类型声明需要发送的字段。创建网站可以携带绑定信息，更新网站只发送网站自身字段。可选返回值显式处理，例如 HTTP 已绑定时的 `operation` 可能为空或省略。 [网站 API](../../../web/src/sites-api.ts) · [服务端创建响应](../../../internal/panel/sites.go)

状态字段用字面量联合类型和实际后端返回值对应。页面按 `Binding.kind` 区分指向类型，用 `certState` 推导显示样式。捕获异常后由 `errorText(e, fallback)` 判断是否为 `Error`，再显示中文原因。 [域名类型](../../../web/src/domain-api.ts) · [证书状态](../../../web/src/cert.ts) · [错误格式化](../../../web/src/format.ts)

可复用组件通过类型化的 `defineProps`、`defineEmits` 和 `defineModel` 公开输入与事件。绑定弹窗用 `done` 携带结果，页面决定刷新或跟踪操作。 [绑定弹窗](../../../web/src/components/BindDomainDialog.vue)
