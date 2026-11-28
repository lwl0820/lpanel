# 后端基础运行契约

## 1. 范围与触发
适用于 Go 模块接入、登录 API、SQLite 与外部命令。以下约定来自 cmd/lpanel/main.go 和 internal/panel/{app,config,auth,operations}.go 的实际代码。

## 2. 签名
- Open(root, version string) (*App, error) 创建 SQLite 和 HTTP 路由；Close 关闭数据库。
- App 暴露 Root、Config、Version、DB、Mux、Command，业务模块在同一 panel 包内接入。
- RequireAuth(next http.Handler) http.Handler 包装需要登录的接口。
- StartOperation(kind string, run func(context.Context) (string, error)) (Operation, error) 执行并保存长操作。
- `lpanel init --root <目录> --username <账号> --port <端口> --listen <回环地址> --repository <owner/repo> [--login-path <路径>]` 从标准输入读取密码。`serve` 和 `version` 为独立子命令。
- 已安装程序提供 `lpanel manage username <用户名>`、`lpanel manage password` 和 `lpanel manage access [--host <域名或IP或auto>] [--port <端口>] [--login-path <路径>] [--https <on或off>] [--agree-tos]`。管理命令可在子命令后指定 `--root <目录>`，默认为 `/opt/lpanel`。 [安装和命令说明](../../../docs/install.md#在服务器终端管理面板)
- 已登录的 `GET /api/v1/system/access` 读取包含 `login_path` 的配置。`PUT /api/v1/system/access/login-path` 接收 `{"login_path":"/abcdef"}` 并返回保存后的完整配置。

## 3. 请求、响应与环境
路由统一 /api/v1。成功直接返回 JSON 对象，不再包一层 data；错误返回 {"error":{"code":"可识别代码","message":"中文说明"}}，由 WriteJSON、WriteError 统一输出，ReadJSON 读取输入。

登录接收 `username`、`password`，返回 `username`。`auth/me` 返回 `username`，`auth/logout` 返回 `ok`。`health` 返回 `status` 和 `version`。操作记录字段为 `id`、`kind`、`status`、`log`、`error`、`created_at`、`updated_at`，实际状态为 `running`、`success`、`failed`。

`login_path` 默认为空，兼容旧配置，`/` 归一为 `""`。定制路径以 `/` 开头，每段使用英文字母、数字、短横线或下划线。保留 `/api`、`/assets` 及其子路径。未启用校验时使用 `POST /api/v1/auth/login`。启用后匿名用户在精确路径以 `GET` 或 `HEAD` 获取登录页，以 `POST` 提交登录，首屏资源由同一入口下的 `/assets/` 提供。有效 Cookie 可访问普通面板路径，登录后的页面响应按会话实时判断。直接访问回环后端的 `/api/v1/health` 供运维使用。

配置位于 Root/data/config.json，数据库为 Root/data/panel.db。后端 Listen 必须为回环地址，且与面板端口不同。Root 为 0755，data 为 0700；公开前端和 ACME 文件不能放进私有 data 供 Nginx worker 读取。

外部命令通过 App.Command 的参数数组执行，Dir 为 Root，TMPDIR 为 Root/.tmp。禁止把用户输入拼接到 shell 命令。

终端管理使用 root 的服务器权限。密码来自交互终端的两次隐藏输入，沿用 8 至 72 字节及 bcrypt 规则。密码更新和会话失效通过同一 SQLite 事务提交。登录插入会话时核对已验证的账号和密码摘要，确保旧凭据随更新失效。

访问设置命令支持组合变更。省略访问选项时输出当前地址。IPv6 在绑定和配置字段中使用裸地址，在 URL 和 HTTP Host 中加方括号。使用已有配置生效标识、面板健康接口和新登录页确认变更成功，然后输出完整地址。失败时恢复先前绑定及访问配置。IP HTTPS 使用相应监听端口的默认 TLS 证书，客户端直接访问 IP 时可以完成握手。域名站点按 SNI 使用自身的证书。

面板公网 IP 证书由 Go ACME 客户端向 Let’s Encrypt 使用 `shortlived` 配置申请。HTTP-01 从公网 80 端口读取 `Root/acme-webroot/.well-known/acme-challenge`。证书及私钥放在签发账户的现有 `live/<IP>` 目录，内置客户端的账户密钥单独存放。续期由 `lpanel-renew.timer` 调度，替换后检查实际提供的证书。普通域名走现有 Certbot 流程。

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
| 非 root 执行账号或访问操作、密码缺少终端或两次输入不同 | 返回中文错误。帮助可直接运行 |
| 开启 HTTPS 时使用自动地址、面板端口为 80 或证书无法验证 | 提示显式指定地址、调整端口或检查公网验证，原登录入口继续使用 |
| 候选 Nginx 或登录入口未生效 | 恢复原配置并返回失败。恢复失败时报告恢复步骤的错误 |

## 5. 正常、基础与失败案例
正常：初始化管理员、登录、携带 Cookie 调用接口、退出后 Cookie 不再有效。
基础：无域名和无证书也可初始化与登录。
失败：已有配置重复执行 init 不覆盖；服务重启后管理员保留。普通运行中操作重启后标记失败，upgrade 由独立工作进程结果决定。

入口启用后，匿名访问 `/abcdef` 可登录，访问 `/` 或 `/abcdef/` 直接返回 404。管理员可持同一 Cookie 将入口改为 `/new-entry`，保存后旧入口失效。提交 `{"login_path":""}` 后恢复根地址登录。

终端可用 `lpanel manage access --host auto --https off --login-path /` 恢复使用服务器地址从根路径登录。服务器对公网提供 80 端口时，可为显式指定的公网 IP 开启 HTTPS。将 HTTPS 面板切换到另一个地址时，先取得新地址的证书，再验证新入口。

## 6. 必需测试
internal/panel/app_test.go 已覆盖登录生命周期、持久化、Secure/过期会话、操作结果、非回环配置和密码不回显。新增业务至少验证未登录、错误输入和关键成功/失败流程。TestMain 已把 TMPDIR 设置为仓库 .tmp/tests，避免 t.TempDir 写系统临时目录。运行 Go 命令时缓存及编译临时目录也应使用仓库 .tmp。

入口回归检查覆盖大小写、尾斜杠、实际登录 POST、旧接口拒绝、会话过期、设置切换和本机探针。浏览器通过 Nginx 验证匿名 404、首屏资源、登录后直接刷新内页及地址变更。

管理命令的定向验证覆盖密码重置后旧凭据和旧会话失效、IPv6 URL、实际端口变更及重启失败恢复。IP 证书测试检查 IP 类型的订单、短期证书配置、验证文件、续期与旧证书保留。公网 Let’s Encrypt 验收与本地测试证书分别记录。

## 7. 错误与正确用法
错误：直接把管理处理函数挂到 Mux，绕开登录；错误使用 /tmp 或 shell 拼接执行用户配置。
正确：Mux.Handle 对管理处理函数套 RequireAuth；命令参数传给 App.Command，文件放在 Root 下的明确目录。共享边界才提取辅助函数，不提前给单次逻辑建立通用层。

入口校验必须覆盖服务端页面和登录请求。启用时公开面板请求通过 Nginx 交给 Go，已有业务 API 继续使用 `RequireAuth`。

管理命令以实际健康及登录页作为切换完成条件。IP 证书续期加载当前的 Nginx 配置并验证当前证书，直接从续期进程的启动配置再次生成入口会覆盖更新后的设置。

## MCP 远程访问

面板设置和远程工具使用两套入口。管理员会话访问 `GET /api/v1/mcp/settings`，返回 `{enabled,path,tokens:[{id,name}]}`。以 `PUT` 提交 `{enabled}` 后返回同一结构。签发时向 `POST /api/v1/mcp/tokens` 发送 `{name}`，响应 201 和 `{token}`。列表只含名称与标识。撤销使用 `DELETE /api/v1/mcp/tokens/{id}`，返回 `{ok:true}`。撤销最后一枚和关闭开关在一个事务中完成。

`mcp_settings` 保存全局开关，初始关闭；`mcp_tokens` 保存独立 ID、名称和令牌的 SHA-256 摘要。每次访问 `/api/v1/mcp` 都重新读取访问状态，包括初始化后对同一客户端发起的工具调用。关闭时返回 404，开启后凭据缺失或失效返回 401 并附带 Bearer 挑战。空令牌列表启用失败时返回 400。已启用时，两枚有效令牌可调用同一组工具。撤销 A 后仍应能用 B 查询。验证重启后这三种状态均持久化。

代理必须将 MCP 精确路径交给面板、保留外部 Host 的端口，并容纳 JSON 转义后的文本。测试应覆盖普通入口、自定义登录路径和托管模板升级。直接复用浏览器会话判断会让已持有令牌的 Agent 在自定义登录入口遭拒。远程工具应通过 Bearer 鉴权后调用明确登记的业务处理器，沿用原操作校验与返回值。文件权限等同宿主机面板管理员。首版按产品决定暂缓 MCP 调用限流。
