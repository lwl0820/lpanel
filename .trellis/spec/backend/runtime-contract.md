# 后端基础运行契约

## 1. 范围与触发
适用于 Go 模块接入、登录 API、SQLite 与外部命令。以下约定来自 cmd/lpanel/main.go 和 internal/panel/{app,config,auth,operations}.go 的实际代码。

## 2. 签名
- Open(root, version string) (*App, error) 创建 SQLite 和 HTTP 路由；Close 关闭数据库。
- App 暴露 Root、Config、Version、DB、Mux、Command，业务模块在同一 panel 包内接入。
- RequireAuth(next http.Handler) http.Handler 包装需要登录的接口。
- StartOperation(kind string, run func(context.Context) (string, error)) (Operation, error) 执行并保存长操作。
- `lpanel init --root <目录> --username <账号> --port <端口> --listen <回环地址> --repository <owner/repo> [--login-path <路径>]` 从标准输入读取密码。`serve` 和 `version` 为独立子命令。
- 已登录的 `GET /api/v1/system/access` 读取包含 `login_path` 的配置。`PUT /api/v1/system/access/login-path` 接收 `{"login_path":"/abcdef"}` 并返回保存后的完整配置。

## 3. 请求、响应与环境
路由统一 /api/v1。成功直接返回 JSON 对象，不再包一层 data；错误返回 {"error":{"code":"可识别代码","message":"中文说明"}}，由 WriteJSON、WriteError 统一输出，ReadJSON 读取输入。

登录接收 `username`、`password`，返回 `username`。`auth/me` 返回 `username`，`auth/logout` 返回 `ok`。`health` 返回 `status` 和 `version`。操作记录字段为 `id`、`kind`、`status`、`log`、`error`、`created_at`、`updated_at`，实际状态为 `running`、`success`、`failed`。

`login_path` 默认为空，兼容旧配置，`/` 归一为 `""`。定制路径以 `/` 开头，每段使用英文字母、数字、短横线或下划线。保留 `/api`、`/assets` 及其子路径。未启用校验时使用 `POST /api/v1/auth/login`。启用后匿名用户在精确路径以 `GET` 或 `HEAD` 获取登录页，以 `POST` 提交登录，首屏资源由同一入口下的 `/assets/` 提供。有效 Cookie 可访问普通面板路径，登录后的页面响应按会话实时判断。直接访问回环后端的 `/api/v1/health` 供运维使用。

配置位于 Root/data/config.json，数据库为 Root/data/panel.db。后端 Listen 必须为回环地址，且与面板端口不同。Root 为 0755，data 为 0700；公开前端和 ACME 文件不能放进私有 data 供 Nginx worker 读取。

外部命令通过 App.Command 的参数数组执行，Dir 为 Root，TMPDIR 为 Root/.tmp。禁止把用户输入拼接到 shell 命令。

## 4. 校验与错误矩阵
| 条件 | 结果 |
| --- | --- |
| 常规模式的受保护 API 未登录或会话过期 | HTTP 401，`unauthorized` |
| 启用入口时匿名访问普通面板路径或公开 API（含旧登录接口） | HTTP 404，`not_found` |
| 登录路径无效或缺失，或 Nginx 未确认生效 | 分别为 HTTP 400 `invalid_login_path` 与 HTTP 500 `login_path_error`，原配置继续使用 |
| 账号密码错误 | HTTP 401，invalid_credentials |
| 请求 JSON 无法解码 | HTTP 400，invalid_json |
| 未知路由或操作 | HTTP 404，not_found |
| 非回环地址、无效端口、前后端端口相同 | Config.Validate 拒绝保存和加载 |
| 重复初始化管理员 | 返回错误，不覆盖已有记录 |

## 5. 正常、基础与失败案例
正常：初始化管理员、登录、携带 Cookie 调用接口、退出后 Cookie 不再有效。
基础：无域名和无证书也可初始化与登录。
失败：已有配置重复执行 init 不覆盖；服务重启后管理员保留。普通运行中操作重启后标记失败，upgrade 由独立工作进程结果决定。

入口启用后，匿名访问 `/abcdef` 可登录，访问 `/` 或 `/abcdef/` 直接返回 404。管理员可持同一 Cookie 将入口改为 `/new-entry`，保存后旧入口失效。提交 `{"login_path":""}` 后恢复根地址登录。

## 6. 必需测试
internal/panel/app_test.go 已覆盖登录生命周期、持久化、Secure/过期会话、操作结果、非回环配置和密码不回显。新增业务至少验证未登录、错误输入和关键成功/失败流程。TestMain 已把 TMPDIR 设置为仓库 .tmp/tests，避免 t.TempDir 写系统临时目录。运行 Go 命令时缓存及编译临时目录也应使用仓库 .tmp。

入口回归检查覆盖大小写、尾斜杠、实际登录 POST、旧接口拒绝、会话过期、设置切换和本机探针。浏览器通过 Nginx 验证匿名 404、首屏资源、登录后直接刷新内页及地址变更。

## 7. 错误与正确用法
错误：直接把管理处理函数挂到 Mux，绕开登录；错误使用 /tmp 或 shell 拼接执行用户配置。
正确：Mux.Handle 对管理处理函数套 RequireAuth；命令参数传给 App.Command，文件放在 Root 下的明确目录。共享边界才提取辅助函数，不提前给单次逻辑建立通用层。

入口校验必须覆盖服务端页面和登录请求。启用时公开面板请求通过 Nginx 交给 Go，已有业务 API 继续使用 `RequireAuth`。
