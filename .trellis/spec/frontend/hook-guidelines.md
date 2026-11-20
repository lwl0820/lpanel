# 组合式逻辑

页面自己的表单、弹窗、选中项和加载状态直接放在 `<script setup>` 中，用 `ref`、`reactive` 和 `computed`。多个页面已经共用的长操作逻辑使用 `useOperation`。可独立运行且已有复用的状态判断或轮询放在普通 `.ts` 模块。 [新建网站表单](../../../web/src/views/SitesView.vue) · [证书状态](../../../web/src/cert.ts)

```ts
const { operation, label, busy, track } = useOperation(projects.operation)
if (await track(op, `部署 ${item.name}`, `${item.name} 部署完成`)) await load()
```

`useOperation` 将状态交给 `OperationBanner`，卸载页面时通过 `AbortController` 停止轮询。`waitOperation` 把 `queued` 和 `running` 当作待完成，直到 `success` 或 `failed`。读取失败或退出页面时 `track` 返回 `null`，调用方只在拿到终态时刷新。 [组合函数](../../../web/src/useOperation.ts) · [轮询实现](../../../web/src/operation.ts) · [轮询测试](../../../web/src/operation.test.ts)

面板升级使用设置页的独立重连流程。面板重启期间的短暂请求中断是升级过程的一部分，继续沿用 `UpgradePanel` 的轮询。 [升级面板](../../../web/src/components/UpgradePanel.vue)
