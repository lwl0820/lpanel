# 错误处理

HTTP 入口使用 `ReadJSON` 解析请求，用 `WriteJSON` 或 `WriteError` 写响应。错误结构是 `{"error":{"code":"invalid_json","message":"请求内容格式错误"}}`。`code` 供调用方区分错误，`message` 面向用户。已有管理接口套用 `RequireAuth`，未登录和过期会话返回 `401`。 [应用公共响应](../../../internal/panel/app.go) · [会话校验](../../../internal/panel/auth.go)

错误文案应让管理员知道可以修正什么。例如重复域名提示「该域名已被使用」，用 SQLite 唯一约束识别此情形。底层失败需要保留原因时用 `fmt.Errorf("…：%w", err)` 包装。涉及密码或 Compose 环境变量时，按已有脱敏规则处理后再返回。 [绑定错误](../../../internal/panel/domains.go) · [项目输出脱敏](../../../internal/panel/projects.go)

部署、证书和升级可能在请求结束后继续执行。启动操作返回操作编号，最终成功或失败写入操作记录。建站时 HTTP 绑定已成功而证书任务无法启动，接口保留网站和 HTTP 入口，并通过 `certificate_error` 提示重试。 [后台操作](../../../internal/panel/operations.go) · [建站响应](../../../internal/panel/sites.go)

配置写入、外部命令和实际生效分别检查。Nginx 重载后回读配置标识，回滚使用有时限且独立于原请求取消的上下文。首次签发失败保留 HTTP，已有有效 HTTPS 的续期失败保留现有入口。 [Nginx 与证书处理](../../../internal/panel/domains.go) · [配置生效测试](../../../internal/panel/domains_test.go)
