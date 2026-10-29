# 前端界面重新设计 · 实施计划

按顺序执行。每步结束时运行“步骤检查”，通过后才进入下一步；任一步失败可回退到上一步结束时的状态。

步骤检查：`npm --prefix web run typecheck && npm --prefix web run lint && npm --prefix web run test -- --run`

## 1. 路由和外壳

- [x] `npm --prefix web install --save-exact vue-router@4.6.4`
- [x] 新建 `src/router.ts`（routes、`createAppRouter`、标题），`main.ts` 挂载 router
- [x] `src/router.test.ts`：用 `createMemoryHistory`，验证未知路径重定向到 `/`、5 条路由的标题
- [x] `App.vue` 改为登录判断 + 外壳 + `RouterView`；旧页面先作为路由组件接入（`views/` 下的新文件暂时包装旧组件），保证每步都能运行
- [x] 检查：步骤检查；`npm --prefix web run dev`，手动确认刷新、前进/后退、未知路径、未登录访问 `/sites` 登录后仍在 `/sites`

## 2. 样式和公共组件

- [x] 重写 `style.css`：原型 `:root` 变量、Element Plus 变量映射、外壳、`.rtable` 与手机卡片、按钮与表单公共类
- [x] `components/`：`AppIcon`、`PageHeader`、`ListState`、`CertPill`、`RowMenu`、`OperationBanner`；`BindingFields.vue` 用 `git mv` 移入
- [x] 外壳按原型完成：白色侧栏、底部账号与退出、≤760px 底部标签栏；`LoginCard`
- [x] 检查：步骤检查；登录页和外壳在 1440 与 390 宽度下与原型一致

## 3. 纯逻辑

- [x] `src/cert.ts` 与 `cert.test.ts`：`daysLeft`（空值、无效日期、当天、已过期）、`certState`（5 种状态，30 天边界）、`overviewTodos`（失败、即将到期、有新版本、无事项）
- [x] `src/operation.ts` 与 `operation.test.ts`（`vi.useFakeTimers`）：`queued → running → success` 才 resolve、`failed` resolve、请求出错 reject、abort 后停止轮询
- [x] 检查：步骤检查

## 4. 页面

每个页面完成后都做一次步骤检查，并在 dev 环境中看加载、空态、错误、有数据四种状态。

- [x] `views/ProjectsView.vue`（`git mv` 后重写）：列表、行菜单、编辑抽屉（含扫描端口）、状态与日志抽屉、长操作
- [x] `components/BindDomainDialog.vue`，接入项目页
- [x] `views/SitesView.vue`（`git mv` 后重写）：列表、网站对话框、绑定域名、删除
- [x] `views/DomainsView.vue`（`git mv` 后重写）：列表、证书状态、编辑 / 申请证书 / 解除、绑定域名（选目标）、面板新地址链接
- [x] `views/SettingsView.vue` + `AccessPanel.vue` + `UpgradePanel.vue`：升级前确认，保留重连与 `sessionStorage`
- [x] `views/OverviewView.vue`：数字、需要处理 / 开始使用、面板访问、错误与重试
- [x] 删除不再使用的 `src/AccessView.vue`、`src/UpgradeView.vue`，以及第 1 步的临时包装

## 5. 验收

- [x] `npm --prefix web run typecheck`、`lint`、`test -- --run`、`build` 全部通过
- [x] 截图：用 `.tmp/shots/` 的 CDP 脚本对 dev 环境截取全部页面（1440×900、390×844），与 `.tmp/shots/proto-*.png` 对比；记录 `scrollWidth - clientWidth` 为 0
- [x] 四种列表状态截图（加载可用接口延迟或断开后端模拟）
- [x] 长操作：部署一个项目，确认 `queued` / `running` 期间不提示成功
- [x] 菜单：底部行向上展开、不被裁剪；Esc 关闭后焦点回到“···”按钮
- [x] 功能清单逐项核对（PRD 验收第 6 条）
- [x] 更新 `.trellis/spec/frontend/directory-structure.md`、`component-guidelines.md`：写入新的目录约定、公共组件和 `waitOperation` 用法

## 审查节点

- 第 2 步结束：外壳和登录页截图给用户看，确认与原型一致后再做页面。
- 第 5 步结束：提交截图和检查结果，由用户验收。

## 回滚

- 回退到上一步结束时的状态（每步结束时工作区都能构建）。丢弃未提交的改动前先与用户确认，不使用 `git reset --hard`。
- 完全放弃：恢复 `web/` 到 `7cfe897`，包括 `package.json` 与 `package-lock.json`。
