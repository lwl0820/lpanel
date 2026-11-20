# 日志与操作输出

CLI 在入口用标准库 `log` 报告启动错误，systemd 安装后的进程日志可用 `journalctl -u lpanel.service` 查看。日常部署、项目启停和证书申请的结果通过 `operations` 表交给面板，包含操作类型、状态、日志、错误和时间。 [CLI 入口](../../../cmd/lpanel/main.go) · [操作记录](../../../internal/panel/operations.go) · [维护命令](../../../docs/install.md)

出错时输出具体失败步骤和必要上下文。外部命令统一由 `App.Command` 接收参数数组和上下文，组合收集标准输出和错误。保留项目日志时先用 `redactProjectOutput` 隐藏在 Compose 环境变量中配置的值。 [执行边界](../../../internal/panel/app.go) · [项目日志与脱敏](../../../internal/panel/projects.go)

安装脚本提供隐藏输入和显式 `--password` 参数。选择显式传参时，密码可能出现在安装脚本的进程参数和调用记录中。两种方式都通过标准输入将密码交给面板初始化。安装输出、普通日志、错误和脚本发起的子进程参数须避免携带密码、会话令牌、证书私钥及项目环境变量值。登录接口响应仅包含用户名。回归用例可复用密码、会话和项目脱敏检查。 [安装说明](../../../docs/install.md#命令行参数) · [安装脚本](../../../deploy/install.sh) · [登录实现](../../../internal/panel/auth.go) · [项目脱敏测试](../../../internal/panel/projects_test.go)
