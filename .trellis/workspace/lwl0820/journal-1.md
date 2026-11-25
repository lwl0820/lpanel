# Journal - lwl0820 (Part 1)

> AI development session journal
> Started: 2026-10-29

---



## Session 1: 服务器管理面板首版实现与提交
<!-- trellis-session: v=2 fp=9a9f4eb7d9bd1745 -->

**Date**: 2026-10-29
**Task**: 服务器管理面板首版实现与提交
**Branch**: `codex/server-management-panel`

### Summary

完成六个模块、独立检查和隔离环境联调，按用户批准创建一个本地工作提交；真实公网证书、systemd、arm64实机及发布源验收继续保留。

### Main Changes

- 实现Go、Vue3和Element Plus面板、Compose、静态网站、Nginx与自动证书、升级和一键安装。
- 修复Nginx重载命令成功但配置未生效时误报HTTPS，补配置标识确认和失败回退。

### Git Commits

| Hash | Message |
|------|---------|
| `7cfe897` | feat: 实现服务器管理面板首版 |

### Testing

- [OK] Go测试与vet、前端类型检查和lint、4项API测试、生产构建、双架构发行包及校验通过。
- [OK] 真实Docker生命周期与保留卷、Nginx前端/API/静态SPA/ACME文件路径/WebSocket双向回显及本地测试证书TLS通过。
- [OK] 安装脚本隔离容器分支测试通过；真实apt/systemd和公网CA不在已验证范围。

### Status

[OK] **Completed**

### Next Steps

- 提供目标服务器、真实发布仓库和域名后完成外部验收，再归档对应任务。
- 保持原有工具配置未提交；会话记录仅本地保存，不增加第二条提交。


## Session 2: 前端界面重新设计（浅色简洁 + vue-router）
<!-- trellis-session: v=2 fp=1b7f59acc68ddbae -->

**Date**: 2026-10-30
**Task**: 前端界面重新设计（浅色简洁 + vue-router）
**Branch**: `feat/frontend-redesign`

### Summary

按 10-29-frontend-optimization 任务完成 LPanel 前端整体改版：浅色简洁风格的侧栏外框与登录页，vue-router history 路由；重写概览、容器项目、静态网站、域名与证书、面板设置五个页面，新增 PageHeader、ListState、RowMenu、CertPill、OperationBanner、BindDomainDialog、AccessPanel、UpgradePanel 等公共组件；长操作统一用 waitOperation/useOperation，queued 不再被当作完成（R23）。新增 cert/format/operation/router 单元测试（22 个），六个页面 CDP 截图脚本 66 项检查通过。补写前端目录结构与组件规范。

### Git Commits

| Hash | Message |
|------|---------|
| `b18753e` | feat: 重新设计前端界面并接入 vue-router |
| `9103590` | docs: 补充前端目录结构与组件规范 |

### Status

[OK] **Completed**


## Session 3: 静态网站默认目录与创建时启用 HTTPS
<!-- trellis-session: v=2 fp=2996721bab6ef8c1 -->

**Date**: 2026-11-20
**Task**: 静态网站默认目录与创建时启用 HTTPS
**Branch**: `main`

### Summary

支持自动分配静态网站目录、指定已有目录，并在新建表单绑定域名和申请 HTTPS。证书申请失败保留 HTTP 入口。

### Git Commits

| Hash | Message |
|------|---------|
| `0876e972bf33443ae85950fac4967b77561e8a93` | feat: 创建静态网站时自动分配目录并支持 HTTPS |

### Testing

- [OK] Go 相关测试和 vet、前端类型检查、ESLint、17 项相关前端测试及生产构建通过。
- [OK] 隔离 Chrome 与 Nginx 联调通过，证书验证使用本地测试证书。

### Status

[OK] **Completed**

### Next Steps

- 在目标服务器验证公网证书签发。


## Session 4: 收尾基础能力与发行部署任务
<!-- trellis-session: v=2 fp=7659b104cde972a1 -->

**Date**: 2026-11-20
**Task**: 收尾基础能力与发行部署任务
**Branch**: `main`

### Summary

依据模块验收记录和实际提交，归档面板基础与登录、静态网站、一键卸载、在线安装命令、版本自动发布五项任务。修复归档后相关任务的文档链接和上下文引用。

### Git Commits

| Hash | Message |
|------|---------|
| `7cfe897` | feat: 实现服务器管理面板首版 |
| `0876e97` | feat: 创建静态网站时自动分配目录并支持 HTTPS |
| `211f618` | docs: 补充在线一键安装命令 |
| `9c48626` | feat: 增加 LPanel 一键卸载 |
| `4ac3eec` | ci: 推送版本标签时自动发布发行包 |

### Testing

- [OK] 复查认证与静态网站 Go 测试、一键卸载冒烟、发布工作流 actionlint 及任务上下文校验。

### Status

[OK] **Completed**

### Next Steps

- 继续验收 Compose 重部署后的代理切换、HTTPS/WSS、公网证书签发和续期，以及目标服务器安装、重启、升级与回退。补齐项目开发规范。


## Session 5: 续收尾开发规范、Compose 项目和面板升级
<!-- trellis-session: v=2 fp=528561b203fecad5 -->

**Date**: 2026-11-20
**Task**: 续收尾开发规范、Compose 项目和面板升级
**Branch**: `main`

### Summary

补齐前后端开发规范，并在独立 Debian 12 amd64 虚拟机通过真实安装、重启、Compose 重部署、代理 HTTPS/WSS、在线升级和失败回退验收。归档开发规范、Compose 和面板升级任务。

### Git Commits

| Hash | Message |
|------|---------|
| `d8a4389` | docs: 补齐 LPanel 前后端开发规范 |
| `77f106dda2ed815f6defc6b874144b63098a8fef` | docs: 记录 Debian 安装、Compose 和升级实测 |

### Testing

- [OK] 核对规范索引、源码引用和任务链接。Go Compose 相关测试通过。
- [OK] 在线安装并重启 v0.1.1。Compose 真实端口由 32811 切换至 32812，测试证书下的 HTTPS、WSS 与删除后保留数据均通过。
- [OK] 通过真实 systemd 工作单元验证 v0.1.0 到 v0.1.1 在线升级、持久化保留和受控启动失败回退。

### Status

[OK] **Completed**

### Next Steps

- 提供可达公网域名，验收签发、续期和安装时启用 HTTPS。继续覆盖其余支持系统及 arm64 实机。


## Session 6: 首版收尾与人工验收规则
<!-- trellis-session: v=2 fp=cc0fdd77f2c45777 -->

**Date**: 2026-11-20
**Task**: 首版收尾与人工验收规则
**Branch**: `main`

### Summary

本地验收已完成，归档域名证书、一键安装及面板总任务。公网与当前本地缺少的环境验收转入人工清单。

### Main Changes

- 把人工验收规则写入前后端项目规范，更新任务归档中的文档与清单引用。

### Git Commits

| Hash | Message |
|------|---------|
| `f72cec5` | docs: 记录真实 Certbot 签发和续期验收 |
| `66f8d3f` | docs: 记录本地验收与人工验收规则 |
| `15e1616` | chore(task): 归档首版剩余三项任务 |

### Testing

- [OK] 核对规范链接、七项首版任务的引用与归档状态。本轮收尾使用已有本地验收记录。

### Status

[OK] **Completed**

### Next Steps

- 由维护者在目标环境执行公网证书、公网 HTTPS 在线安装、其余系统和 arm64 实机验收。


## Session 7: 安装脚本支持命令行配置
<!-- trellis-session: v=2 fp=19292dd4856b9271 -->

**Date**: 2026-11-20
**Task**: 安装脚本支持命令行配置
**Branch**: `main`

### Summary

安装脚本可直接接收账号、密码、端口、域名和证书选项，仅在缺少适用配置时交互。

### Main Changes

- 同步安装帮助、README、安装说明、部署约定和密码传递约定。

### Git Commits

| Hash | Message |
|------|---------|
| `dbcc65d5e06ba430c7ca9cf69b96fbde19606522` | feat: 支持安装脚本命令行参数 |

### Testing

- [OK] Bash 语法、帮助和文档链接通过检查。安装冒烟用例在无网络本地命名空间通过，系统服务和证书使用替身。

### Status

[OK] **Completed**

### Next Steps

- 公网 HTTPS 安装由维护者在目标服务器人工验收。


## Session 8: 调整安装脚本证书默认值
<!-- trellis-session: v=2 fp=16b06298bdf5e8a7 -->

**Date**: 2026-11-20
**Task**: 调整安装脚本证书默认值
**Branch**: `main`

### Summary

填写面板域名后，申请证书和同意条款均默认选 y，联系邮箱默认使用 acme@面板域名。

### Git Commits

| Hash | Message |
|------|---------|
| `72903627a9ca5b9b163f88f5c384bcccc074572b` | feat: 调整安装脚本证书默认值 |

### Testing

- [OK] Bash 语法检查及独立 Linux 命名空间中的安装冒烟测试通过，证书签发使用测试替身。

### Status

[OK] **Completed**


## Session 9: 修复安装脚本的面板健康检查
<!-- trellis-session: v=2 fp=e87e9a8adb427f0f -->

**Date**: 2026-11-20
**Task**: 修复安装脚本的面板健康检查
**Branch**: `main`

### Summary

安装完成前通过 Nginx 的面板端口检查后端健康状态，连接失败时静默重试，持续失败时提示查看服务日志。

### Git Commits

| Hash | Message |
|------|---------|
| `d306348a0a91b0292647082efc439d8e5a87e814` | fix: 修复安装脚本的面板健康检查 |

### Testing

- [OK] Bash 语法与差异检查通过。Docker 不可用，安装冒烟用例在隔离 Linux 命名空间中使用命令替身通过，覆盖自定义端口、首次失败后重试、HTTPS 入口和证书失败回退。
- [OK] 另以命令替身验证持续探测失败以及返回无效响应时退出，真实服务器部署尚未验证。

### Status

[OK] **Completed**


## Session 10: 支持多个 ACME 证书账户
<!-- trellis-session: v=2 fp=e6d4f682579b6223 -->

**Date**: 2026-11-20
**Task**: 支持多个 ACME 证书账户
**Branch**: `main`

### Summary

安装与面板支持 Let’s Encrypt 和 ZeroSSL，可按证书选择账户并沿用签发账户续期。默认账户自动接任，仍有已签发证书的账户禁止删除。

### Git Commits

| Hash | Message |
|------|---------|
| `b7d953c` | feat: 支持多个 ACME 证书账户 |

### Testing

- [OK] 相关 Go 和前端测试、安装冒烟及 Chrome 与 Nginx 联调通过。联调用 Certbot 替身，公网 CA 签发待实测。

### Status

[OK] **Completed**


## Session 11: 支持面板专用登录入口
<!-- trellis-session: v=2 fp=a167ff26f9c101cb -->

**Date**: 2026-11-20
**Task**: 支持面板专用登录入口
**Branch**: `main`

### Summary

管理员可设置专用登录路径，匿名访问其他面板地址直接返回 404，有效会话可照常访问内页。安装脚本新增可留空的 --login-path。

### Git Commits

| Hash | Message |
|------|---------|
| `c53c396` | feat: 支持面板专用登录入口 |

### Testing

- [OK] 相关 Go、前端和安装冒烟通过。隔离 Chrome 与真实 Nginx 验证入口切换、会话状态、重启保留、HTTPS 跳转、ACME challenge 和业务站点。HTTPS 使用测试证书。

### Status

[OK] **Completed**


## Session 12: 完善静态网站创建与备注编辑
<!-- trellis-session: v=2 fp=df4b8fe3fd59acc6 -->

**Date**: 2026-11-20
**Task**: 完善静态网站创建与备注编辑
**Branch**: `main`

### Summary

新建站点支持多主机名、唯一网站标识和路径安全的默认目录，支持创建与编辑备注。通配符提供 HTTP，普通域名逐个申请 HTTPS。后端与前端定向检查、Chrome 和真实 Nginx 联调通过，证书使用本地测试证书。

### Git Commits

| Hash | Message |
|------|---------|
| `4e5b4dc` | feat: 完善静态网站创建与备注编辑 |

### Status

[OK] **Completed**


## Session 13: 新建 Compose 项目后自动部署
<!-- trellis-session: v=2 fp=fae97fc363539a73 -->

**Date**: 2026-11-21
**Task**: 新建 Compose 项目后自动部署
**Branch**: `main`

### Summary

新建项目点击「创建」即自动部署，列表显示进度和结果；部署失败保留项目，可修改配置后重试。

### Main Changes

- 复用现有后台部署操作，创建接口返回部署任务，编辑配置仅保存。
- 更新容器项目页面、接口契约和验收记录。

### Git Commits

| Hash | Message |
|------|---------|
| `2b0d30d` | feat: 新建 Compose 项目后自动部署 |

### Testing

- [OK] Go 项目定向测试、Go 静态检查、前端 ESLint、轮询测试及生产构建通过。
- [OK] Chrome 隔离验收通过。配置由实际 Compose CLI 校验，容器启动使用命令替身。

### Status

[OK] **Completed**

### Next Steps

- 在运行 Docker 的测试服务器验收真实容器启动和发布端口访问。


## Session 14: 优化证书提供商选择与账户注册
<!-- trellis-session: v=2 fp=f8c37dc37eec5635 -->

**Date**: 2026-11-21
**Task**: 优化证书提供商选择与账户注册
**Branch**: `main`

### Summary

添加证书账户使用下拉框，ZeroSSL 可用邮箱自动注册并支持手动重试，Google Cloud Public CA 使用项目生成的绑定凭据。

### Git Commits

| Hash | Message |
|------|---------|
| `0035094` | feat: 优化证书提供商选择与账户注册 |

### Testing

- [OK] Go ACME 回归测试、前端类型检查、ESLint 和生产构建通过。Chrome 隔离验收覆盖下拉键盘操作、凭据校验与重试。

### Status

[OK] **Completed**

### Next Steps

- 有公网测试域名和 Google Cloud 项目凭据时验证真实签发与续期。


## Session 15: 容器项目列表、单容器日志与文件清理
<!-- trellis-session: v=2 fp=b286a0af64b00ed2 -->

**Date**: 2026-11-21
**Task**: 容器项目列表、单容器日志与文件清理
**Branch**: `main`

### Summary

项目列表显示运行状态，可展开查看容器并分别读取日志。删除项目时可选清理该项目的目录。

### Main Changes

- 项目行直接显示启停、重启和编辑配置。
- 新增单容器日志与默认关闭的文件清理选项，保护命名卷、目录外文件和实际挂载点。

### Git Commits

| Hash | Message |
|------|---------|
| `e50a051` | feat: 完善容器项目列表、日志与文件清理 |

### Testing

- [OK] 独立提交范围内的 Go 项目测试、Go 静态检查、前端类型检查和 ESLint 通过。
- [OK] 宽窄屏浏览器验收和实际挂载点保留验证通过，容器操作使用替身。

### Status

[OK] **Completed**

### Next Steps

- 在运行 Docker 的隔离环境验收多个容器副本的日志与实机命名卷保留。


## Session 16: 面板命令行管理和公网 IP HTTPS
<!-- trellis-session: v=2 fp=d0f58c53ed3bfd9d -->

**Date**: 2026-11-21
**Task**: 面板命令行管理和公网 IP HTTPS
**Branch**: `main`

### Summary

实现面板命令行管理和内置公网 IPv4、IPv6 证书申请及续期。

### Main Changes

- 管理员可在终端修改账号凭据和访问设置，切换失败时恢复原入口。
- IP 证书使用内置 Go ACME 客户端申请和续期。

### Git Commits

| Hash | Message |
|------|---------|
| `a41d6cf` | feat: 添加面板管理命令及公网 IP HTTPS |

### Testing

- [OK] Go 定向回归、前端类型检查、指定文件 ESLint、Vitest 和生产构建通过。
- [OK] 隔离环境的真实 Nginx 验证了 IPv4、IPv6 HTTPS、同端口域名证书和失败回退。

### Status

[OK] **Completed**

### Next Steps

- 在真实公网和 systemd 环境验收 Let’s Encrypt 签发和续期。


## Session 17: 交付服务器状态监控
<!-- trellis-session: v=2 fp=51fc2d77d526ce8e -->

**Date**: 2026-11-22
**Task**: 交付服务器状态监控
**Branch**: `main`

### Summary

首页显示整机资源摘要。详情提供各磁盘用量、实时进程排序和最近 30 天的分钟趋势。在 Ubuntu 隔离机核对后台采样、重启留存、停机断线、到期清理和手机布局，验收记录见 docs/validation.md。

### Git Commits

| Hash | Message |
|------|---------|
| `3380fd3` | feat: 增加服务器状态监控 |

### Status

[OK] **Completed**


## Session 18: 单容器启停和本地镜像管理
<!-- trellis-session: v=2 fp=1a61fc942ae10aaa -->

**日期** 2026-11-22
**任务** 单容器启停和本地镜像管理
**分支** `main`

### 摘要

核对并落实两条远端 issue，完成浏览器和真实 Docker 验收。

### 主要改动

- 项目支持按容器 ID 启停单个副本，镜像页支持删除和清理闲置镜像。

### Git 提交

| 提交 | 说明 |
|------|---------|
| `b571a7e` | feat: 支持单容器启停和本地镜像管理 |

### 验证

- 定向 Go 测试、8 条路由用例、ESLint、类型检查、构建、Chrome 交互与隔离虚拟机 Docker 验收通过。

### 状态

**已完成**


## Session 19: Compose 项目目录与旧版本升级
<!-- trellis-session: v=2 fp=2bf16b714d5e08f3 -->

**Date**: 2026-11-23
**Task**: Compose 项目目录与旧版本升级
**Branch**: `codex/compose-project-folder`

### Summary

新建项目按名称登记目录，升级回填旧路径，支持复用保留的业务文件。

### Git Commits

| Hash | Message |
|------|---------|
| `990cc9c` | feat: 按项目名称管理新建 Compose 项目目录 |

### Testing

- [OK] 真实 Docker 虚拟机验收通过，覆盖旧容器升级、改名后挂载、部分与整目录删除及恢复。
- [OK] 项目定向 Go 测试、竞态检查、go vet、前端检查与构建、Chrome 表单验证及卸载冒烟测试通过。

### Status

[OK] **Completed**


## Session 20: 面板自动更新
<!-- trellis-session: v=2 fp=e636b84b25c38a61 -->

**Date**: 2026-11-23
**Task**: 面板自动更新
**Branch**: `codex/compose-project-folder`

### Summary

增加持久化自动更新开关，复用每日检查和升级流程；覆盖旧库迁移、安装条件和页面交互。

### Git Commits

| Hash | Message |
|------|---------|
| `00097a8` | feat: 支持面板后台自动更新 |

### Status

[OK] **Completed**


## Session 21: 编排项目迁移与容器管理导航
<!-- trellis-session: v=2 fp=2921ba6396993f92 -->

**Date**: 2026-11-23
**Task**: 编排项目迁移与容器管理导航
**Branch**: `codex/compose-project-folder`

### Summary

完成完整项目目录 ZIP 迁移、导入即部署和容器管理双标签入口，通过浏览器、真实 Nginx 和 Docker 验收。

### Git Commits

| Hash | Message |
|------|---------|
| `5525f6a` | feat: 支持编排项目跨服务器迁移并合并容器管理入口 |

### Status

[OK] **Completed**


## Session 22: 从编排项目打开项目目录
<!-- trellis-session: v=2 fp=1a3914f20522c4bf -->

**Date**: 2026-11-24
**Task**: 从编排项目打开项目目录
**Branch**: `codex/open-compose-project-directory`

### Summary

在浏览器验收了不同项目的真实目录字段、中文及加号、旧目录失败后重试、返回与侧栏根目录。

### Git Commits

| Hash | Message |
|------|---------|
| `b37c73f` | feat: 从编排项目直接打开文件管理中的项目目录 |

### Status

[OK] **Completed**


## Session 23: 编排项目绑定域名支持自定义端口
<!-- trellis-session: v=2 fp=6c2e80e8424aa880 -->

**Date**: 2026-11-24
**Task**: 编排项目绑定域名支持自定义端口
**Branch**: `main`

### Summary

自定义宿主机端口随项目保存，可由多个域名复用并随项目压缩包迁移。

### Git Commits

| Hash | Message |
|------|---------|
| `3687847` | feat: 支持编排项目使用自定义宿主机端口绑定域名 |

### Testing

- [OK] 在隔离虚拟机运行真实 Docker 和 Nginx，验证 host 网络及动态映射共存、重部署和移除端口后的域名访问。
- [OK] 项目与绑定 Go 测试、前端类型与构建检查及 Chrome 操作验收通过。

### Status

[OK] **Completed**


## Session 24: 绑定域名时自动发现编排项目的运行端口
<!-- trellis-session: v=2 fp=2a74e998344caf74 -->

**Date**: 2026-11-25
**Task**: 绑定域名时自动发现编排项目的运行端口
**Branch**: `main`

### Summary

域名绑定弹窗读取 Docker 实际发布映射和项目所属 host 容器进程的 TCP 监听，继续支持手动端口。

### Git Commits

| Hash | Message |
|------|---------|
| `528f54e` | feat: 绑定域名时自动发现编排项目的运行端口 |

### Testing

- [OK] 隔离虚拟机验证两个项目的端口归属、子进程监听、Nginx 实际访问及动态端口重部署。
- [OK] 项目与绑定 Go 回归测试、go vet、前端类型检查、ESLint、生产构建及 Chrome 交互验收通过。

### Status

[OK] **Completed**


## Session 25: 编辑编排域名绑定的端口和上游协议
<!-- trellis-session: v=2 fp=e750135ab4038bbe -->

**Date**: 2026-11-25
**Task**: 编辑编排域名绑定的端口和上游协议
**Branch**: `main`

### Summary

域名与证书页支持原地编辑编排项目的上游协议和端口，保留同域名证书。

### Git Commits

| Hash | Message |
|------|---------|
| `4b19666` | feat: 支持编辑编排域名绑定的端口和上游协议 |

### Testing

- [OK] 定向 Go 测试、前端检查和 Chrome 编辑流程通过。隔离 Docker 和 Nginx 验证了原域名 HTTPS 访问、端口切换、动态映射刷新与失败回滚。

### Status

[OK] **Completed**


## Session 26: 编排项目升级镜像
<!-- trellis-session: v=2 fp=82abfa63354892c8 -->

**Date**: 2026-11-25
**Task**: 编排项目升级镜像
**Branch**: `codex/compose-upgrade-images`

### Summary

项目行更多菜单支持强制拉取镜像，更新运行中的服务并同步域名的实际端口。

### Main Changes

- 按项目镜像引用和平台拉取，比较容器镜像 ID 后定向重建。

### Git Commits

| Hash | Message |
|------|---------|
| `7f4c30e` | feat: 支持编排项目升级镜像 |

### Testing

- [OK] 定向 Go 回归、前端类型检查及浏览器交互通过。隔离 Docker 与 Nginx 实测同标签更新、停止服务保留、多架构镜像判断及域名刷新重试。

### Status

[OK] **Completed**


## Session 27: 补全编排项目反代请求头
<!-- trellis-session: v=2 fp=0d737d4ef7020b40 -->

**Date**: 2026-11-25
**Task**: 补全编排项目反代请求头
**Branch**: `main`

### Summary

编排项目向上游传递一致的单值客户端地址，普通 HTTP 和 WebSocket 共用项目入口。

### Git Commits

| Hash | Message |
|------|---------|
| `0e4983e` | fix: 补全编排项目反代的来源和连接头 |

### Testing

- [OK] 定向 Go 测试和 vet 通过。真实 Nginx 1.24 在两个域名上通过配置检查、来源地址覆盖与 WebSocket 101 握手验证。

### Status

[OK] **Completed**


## Session 28: 宿主机程序管理与 B 版界面验收
<!-- trellis-session: v=2 fp=0ec652cb01d8ae45 -->

**Date**: 2026-11-25
**Task**: 宿主机程序管理与 B 版界面验收
**Branch**: `main`

### Summary

接入 Supervisor、程序日志和域名绑定，完成桌面与手机界面对照、真实服务联调、LPanel 升级与回退验证。

### Git Commits

| Hash | Message |
|------|---------|
| `4f6de5c` | feat: 支持通过 Supervisor 托管宿主机程序 |

### Status

[OK] **Completed**


## Session 29: 文件管理批量操作与分片上传
<!-- trellis-session: v=2 fp=2885dabd8ab1ddf5 -->

**Date**: 2026-11-25
**Task**: 文件管理批量操作与分片上传
**Branch**: `main`

### Summary

支持批量文件操作、权限和属主修改，点选、拖入及本机粘贴文件均支持分片和进度。

### Git Commits

| Hash | Message |
|------|---------|
| `3003a65` | feat: 增强文件批量操作和分片上传 |

### Testing

- [OK] 通过文件管理 Go 回归、go vet、前端相关测试、类型检查、ESLint 与生产构建。
- [OK] 验证真实 Dolphin 到 Chrome 的粘贴、1 MiB 代理限制下的分片上传与摘要、跨盘元数据保留及 root 更改属主。

### Status

[OK] **Completed**
