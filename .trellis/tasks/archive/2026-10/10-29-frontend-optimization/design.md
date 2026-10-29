# 前端界面重新设计 · 技术设计

参考：`prd.md`（R1–R25）、原型 `.trellis/tasks/10-29-frontend-optimization/research/prototype.html`、`.trellis/spec/frontend/api-contract.md`。

## 边界

- 只改 `web/`：`package.json` / `package-lock.json`（新增 vue-router）、`src/` 下的页面、组件、样式和测试。
- 不改 `*-api.ts` 的接口签名和 `api.ts`，不改后端、nginx 配置和构建脚本。
- 生产环境由 nginx 托管面板，`internal/panel/domains.go:298` 已有 `try_files $uri $uri/ /index.html`，history 模式无需后端配合；Vite 开发服务器默认也有 SPA 回退。

## 依赖

- `vue-router` 固定为 `4.6.4`（`--save-exact`，与现有依赖的固定写法一致）。
- 不选 5.x（latest 5.4.0）：5.x 把基于文件的路由并入主包，依赖 unplugin、chokidar 等构建期工具，本项目只需 5 条手写路由用不到；两者运行时 API 相同（`createRouter`、`createWebHistory`、`RouterView`），以后升级到 5.x 不需要改代码。
- 不新增图标库、CSS 框架或状态库。

## 文件布局

现有文件平铺在 `src/`，`views/` 为空目录。重写后：

```
src/
  main.ts                 挂载 router
  App.vue                 登录判断 + 外壳（侧栏 / 手机底栏 + RouterView）
  router.ts               routes、createAppRouter(history)
  style.css               设计变量、Element Plus 变量映射、公共类（重写）
  cert.ts                 证书状态、剩余天数、概览待处理事项（纯函数）
  operation.ts            waitOperation 轮询（纯 TS）
  api.ts、*-api.ts        不变
  components/
    AppIcon.vue           内联 SVG 图标（原型中的图标集合）
    PageHeader.vue        H1 + 说明 + 主操作插槽（R5）
    ListState.vue         加载 / 错误+重试 / 空态（图标、标题、说明、主操作）
    CertPill.vue          证书状态标签
    RowMenu.vue           “···”菜单（封装 el-dropdown）
    OperationBanner.vue   长操作进行中 / 失败 + 日志展开
    BindingFields.vue     从 src/ 移入，界面按新样式调整
    BindDomainDialog.vue  绑定 / 编辑域名（项目、网站、面板）
    LoginCard.vue
    AccessPanel.vue       由 AccessView.vue 重写
    UpgradePanel.vue      由 UpgradeView.vue 重写
  views/
    OverviewView.vue  ProjectsView.vue  SitesView.vue  DomainsView.vue  SettingsView.vue
  *.test.ts               api.test.ts（不变）、cert.test.ts、operation.test.ts、router.test.ts
```

`ProjectsView.vue`、`SitesView.vue`、`DomainsView.vue` 用 `git mv` 移入 `views/` 后重写，保留文件历史。项目编辑抽屉和日志抽屉先写在 `ProjectsView.vue` 内；超过约 250 行再拆为 `components/ProjectDrawer.vue`、`LogsDrawer.vue`。

## 路由与登录（R6、R7）

```ts
// router.ts
export const routes: RouteRecordRaw[] = [
  { path: '/', component: () => import('./views/OverviewView.vue'), meta: { title: '概览' } },
  { path: '/projects', component: () => import('./views/ProjectsView.vue'), meta: { title: '容器项目' } },
  { path: '/sites', component: () => import('./views/SitesView.vue'), meta: { title: '静态网站' } },
  { path: '/domains', component: () => import('./views/DomainsView.vue'), meta: { title: '域名与证书' } },
  { path: '/settings', component: () => import('./views/SettingsView.vue'), meta: { title: '面板设置' } },
  { path: '/:pathMatch(.*)*', redirect: '/' },
]
export function createAppRouter(history = createWebHistory()) { /* createRouter + afterEach 设置 `${title} · LPanel` */ }
```

- 登录状态仍在 `App.vue`：`user` 为空时渲染 `LoginCard`，不渲染 `RouterView`；router 照常解析当前地址，所以登录成功后自然停留在原路径。不使用路由守卫，避免守卫和 App 各维护一份登录状态。
- 未登录时 `document.title` 设为“登录 · LPanel”；登录后由 `afterEach` 的结果接管（登录时用 `router.currentRoute` 补设一次）。
- 退出：`auth.logout()` 成功后 `user = null` 并 `router.replace('/')`，与现在退出回到概览的行为一致。
- 侧栏与底栏用 `RouterLink`，激活态由 `active-class` 控制，`/` 使用精确匹配。
- 页面内跳转（概览的数字卡片、“开始使用”、“有新版本”）用 `router.push`。

## 样式（R1–R4）

- `style.css` 的 `:root` 照搬原型的设计变量（颜色、字号、圆角、阴影、字体）。
- Element Plus 变量映射到同一套取值，写在 `style.css`，位于 `element-plus/dist/index.css` 之后加载（`main.ts` 现有顺序已满足）：
  - `--el-color-primary: var(--accent)`；`--el-color-primary-light-3/5/7/8/9` 和 `-dark-2` 用 `color-mix(in srgb, var(--accent) N%, white|black)` 计算。
  - success / warning / danger 映射到 `--ok` / `--warn` / `--bad`；文字、边框、背景、圆角、`--el-font-family`、`--el-font-size-base: 14px` 同步映射。
- 列表用原生 `<table class="rtable">`，单元格带 `data-label`；≤760px 时用 CSS 把行变成卡片（与原型相同的规则），不使用 `el-table`，因为它的固定列布局无法改成卡片。
- 抽屉：`el-drawer`，`size="min(720px, 100vw)"`，手机宽度下即为全屏。对话框：`width="min(560px, calc(100vw - 32px))"`。
- 组件内样式只写布局；颜色一律引用变量，不再写死十六进制值。

## 公共组件

- `RowMenu`：封装 `el-dropdown`（`trigger="click"`、默认 teleport 到 body、popper 自动翻转），所以底部行的菜单向上展开且不被表格裁剪。触发按钮带 `aria-label="更多操作"`。验收要求 Esc 关闭后焦点回到触发按钮；如 `el-dropdown` 达不到，改为按原型的 `openMenu/closeMenu` 自行实现，接口不变（`items: { label, danger?, disabled?, onSelect }[]`）。
- `ListState`：props `loading`、`error`、`empty`，emit `retry`；插槽 `empty-action` 放主操作按钮。有数据时渲染默认插槽。
- `OperationBanner`：props `operation: Operation | null`、`label`（如“正在部署 blog”）。`queued` / `running` 显示进行中条；`failed` 显示错误原因和“查看日志”（展开 `operation.log`，深色等宽面板）；`success` 不显示（由调用方弹成功提示）。
- `CertPill`：输入 `CertState`，输出有效 / 即将到期 / 申请中 / 申请失败 / 未启用 标签。

## 纯逻辑

`cert.ts`：

```ts
export type CertState = 'valid' | 'expiring' | 'requesting' | 'failed' | 'disabled'
export function daysLeft(expiresAt: string, now: Date): number | null   // 空值或无效日期返回 null，向下取整
export function certState(b: Binding, now: Date): CertState            // enabled 且 ≤30 天为 expiring
export interface Todo { kind: 'cert-failed' | 'cert-expiring' | 'upgrade'; binding?: Binding; days?: number }
export function overviewTodos(bindings: Binding[], upgrade: UpgradeState, now: Date): Todo[]
```

`now` 由调用方传入，测试不依赖系统时间。概览的数字（R9）也用这些函数计算。

`operation.ts`：

```ts
export function waitOperation(get: (id: string) => Promise<Operation>, op: Operation,
  opts: { interval?: number; signal?: AbortSignal; onUpdate?: (op: Operation) => void }): Promise<Operation>
```

- `queued`、`running` 继续轮询（默认 1500ms），`success`、`failed` 时 resolve；请求出错时 reject；`signal` 中止时 reject 一个 `AbortError`，页面卸载时调用方 abort 并忽略该错误。
- 项目操作、证书申请（网站绑定、域名页、概览）都用它，修复 `DomainsView.vue` 只认 `running` 的问题（R23）。
- 升级轮询不使用它：升级期间请求失败表示面板正在重启，需要“重新连接”而不是报错；`UpgradePanel` 保留现有的 3 秒轮询、`sessionStorage['lpanel-upgrade']` 和重连逻辑，只换界面。

## 页面数据流

- **概览**：`Promise.all([projects.list(), sites.list(), domains.list(), domains.access(), upgrades.status()])`。任一失败时整块显示错误和重试，不显示数字（R13）。项目绑定数、网站 SPA 数、HTTPS 数、待处理数由列表和 `cert.ts` 计算。“重新申请” / “续期”调用 `domains.certificate(id)` 后 `waitOperation`，完成后重新加载。
- **容器项目**：`projects.list()` + `domains.list()`（显示已绑定域名）。行内露出“部署”，菜单里是编辑、启动、停止、重启、状态与日志、绑定域名、删除。操作期间整页 `busy`，所有行操作禁用（与现在相同），`OperationBanner` 显示进度；结束后重新加载列表。
- **静态网站**：`sites.list()` + `domains.list()`；露出“编辑”，菜单里是绑定域名、删除。
- **域名与证书**：`domains.list()`，另取 `projects.list()`、`sites.list()` 用于显示指向的项目 / 网站名称。菜单：编辑、申请证书 / 续期、解除（面板域名不显示解除，R18）。主操作“绑定域名”打开 `BindDomainDialog`（可选目标）。面板域名保存或申请证书后，沿用现有逻辑计算并显示“打开面板新地址”链接（R24）。
- **面板设置**：`AccessPanel`（只编辑 `acme_email`、`agree_tos`，提交时带回读取到的 `acme_server` 原值）+ `UpgradePanel`；“升级到 vX”先确认。

## BindDomainDialog（R22）

- props：`open`、`preset?: { kind: 'project' | 'site' | 'panel'; id: string }`、`binding?: Binding`（编辑模式）；emit `done(result: { operation: Operation | null; accessURL: string })`。
- 新建且没有 `preset`（域名页）：先选目标类型和具体项目 / 网站，列表打开时加载。
- 项目：`projects.get` + `projects.scan`，只列 `source === 'ports' && protocol === 'tcp'` 的端口，保留 http/https 选项，调用 `projects.bind`；没有可用端口时显示现有提示。
- 网站：`sites.bind`。面板与编辑模式：`domains.save`。
- 校验：域名必填、项目必须选端口，错误显示在字段下方（`el-form` rules）。
- 对话框只负责提交；返回的 `operation` 由调用页交给 `waitOperation` 和 `OperationBanner`。

## 兼容性

- 旧版只有 `/`，没有已存在的深链接；升级后旧书签仍落在概览。
- `sessionStorage['lpanel-upgrade']` 的键和含义不变，升级途中刷新仍能接上进度；升级成功后的“刷新面板”在当前路径上 reload。
- 接口调用、请求参数、确认文案与现有实现一致；功能清单按 PRD 验收项逐项核对。

## 回滚

- 改动只在 `web/`，没有数据或接口迁移。回滚即回退本任务的提交并重新 `npm --prefix web run build`。
- `implement.md` 每一步结束时工作区都能通过 typecheck 和 build，可以停在任一步回退。
