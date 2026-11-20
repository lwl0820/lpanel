# 日志与操作输出

CLI 在入口用标准库 `log` 报告启动错误，systemd 安装后的进程日志可用 `journalctl -u lpanel.service` 查看。日常部署、项目启停和证书申请的结果通过 `operations` 表交给面板，包含操作类型、状态、日志、错误和时间。 [CLI 入口](../../../cmd/lpanel/main.go) · [操作记录](../../../internal/panel/operations.go) · [维护命令](../../../docs/install.md)

出错时输出具体失败步骤和必要上下文。外部命令统一由 `App.Command` 接收参数数组和上下文，组合收集标准输出和错误。保留项目日志时先用 `redactProjectOutput` 隐藏在 Compose 环境变量中配置的值。 [执行边界](../../../internal/panel/app.go) · [项目日志与脱敏](../../../internal/panel/projects.go)

安装初始化从标准输入传入管理员密码，登录接口响应仅包含用户名。用户可见错误、普通日志和进程参数应避免携带密码、会话令牌、证书私钥及项目环境变量值。回归用例可复用密码、会话和项目脱敏检查。 [安装脚本](../../../deploy/install.sh) · [登录实现](../../../internal/panel/auth.go) · [项目脱敏测试](../../../internal/panel/projects_test.go)
