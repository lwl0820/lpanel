# 组件规范

## 文件结构
`<script setup lang="ts">` 在前，`<template>` 在后。不写 scoped 样式，共用 class 都在 `src/style.css`。注释用简短中文说明意图。

## Props 与事件
- 用 `defineProps<{ ... }>()`、`defineEmits<{ name: [参数] }>()` 声明类型；对话框的打开状态用 `defineModel`（`v-model:open`）。
- 父组件需要的类型从组件导出，例如 `RowMenu.vue` 的 `MenuItem`、`BindDomainDialog.vue` 的 `BindPreset` / `BindResult`。

## 公共组件
| 组件 | 用途 |
| --- | --- |
| `PageHeader` | 页面标题和说明，按钮放默认插槽 |
| `ListState` | 加载 / 错误（带重试）/ 空态，有数据时渲染默认插槽；`empty` 及空态文字可省略 |
| `RowMenu` | 行内“···”菜单，基于 `el-dropdown`；菜单项 `{ label, icon, danger?, divided?, onSelect }` |
| `CertPill` | 按 `certState()` 显示证书状态 |
| `OperationBanner` | 显示排队中 / 进行中 / 失败的长操作和日志入口，成功时不显示 |
| `BindDomainDialog` | 为项目、网站或面板绑定 / 编辑域名，完成后 emit `done({ operation, accessURL, domain })` |
| `AccessPanel`、`UpgradePanel` | 面板设置页的两块 |

## 长操作
部署、启动 / 停止 / 重启 / 删除项目、申请证书都用 `useOperation(getter)`，把返回的 `operation`、`label` 交给 `OperationBanner`：

```ts
const { operation, label, busy, track } = useOperation(projects.operation)
if (await track(op, `部署 ${item.name}`, `${item.name} 部署完成`)) await load()
```

- `queued` 和 `running` 都算未完成，拿到最终结果后才提示成功（R23）。
- `track` 在读取失败或页面离开（取消）时返回 `null`，只有非空结果才重新加载列表。
- 操作期间用 `busy` 禁用行操作。
- 例外：面板升级仍在 `UpgradePanel` 里每 3 秒轮询，因为升级期间请求失败表示面板正在重启，需要显示“重新连接”而不是报错。

## 列表与表单
- 列表用 `.panel` 内的 `table.rtable`，单元格带 `data-label`，760px 以下自动变卡片。常用操作直接显示，其余放进 `RowMenu`。项目列表直接显示启停、重启和「编辑」按钮，窄屏允许换行。
- 表单用 `el-form`、`label-position="top"` 和校验规则；文字字段的规则用 `trigger: ['blur', 'change']`，改正后错误立即消失。
- 对话框 `width="min(520px, calc(100vw - 32px))"`、`append-to-body`；抽屉 `size="min(720px, 100vw)"`、`class="app-drawer"`。
- 删除、解除绑定先用危险操作确认。容器项目删除使用含文件清理选项的对话框，默认焦点落在「取消」。只需文字确认时使用 `ElMessageBox.confirm`，确认按钮 `confirmButtonClass: 'el-button--danger'`。

## 样式
颜色和尺寸取自 `style.css` 中 `:root` 的变量（只有浅色主题，一个蓝色强调色）。Element Plus 的变量已映射到同一组值，优先用组件属性，少覆盖它的 class。

## 无障碍
只有图标的按钮要加 `aria-label`（和 `title`）。进行中的区域用 `role="status"`，错误用 `role="alert"`。菜单和对话框关闭后焦点回到触发按钮（Element Plus 默认如此）。

## 常见错误
- 关闭的 `el-dialog` 仍留在 DOM 中，测试脚本要选可见的那个（`offsetParent !== null`）。
- `OperationBanner` 已显示失败原因时，不要再弹错误提示。
