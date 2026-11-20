# 面板命令行管理工具设计

## 命令入口

使用发行包内已有的程序和安装路径。安装文档提供以下命令，已有安装通过升级取得同一入口。

| 命令 | 行为 |
| --- | --- |
| `sudo /opt/lpanel/current/lpanel manage --help` | 查看中文命令帮助 |
| `sudo /opt/lpanel/current/lpanel manage username <用户名>` | 修改管理员用户名 |
| `sudo /opt/lpanel/current/lpanel manage password` | 在终端隐藏输入并确认新密码 |
| `sudo /opt/lpanel/current/lpanel manage access` | 查看当前登录地址 |
| `sudo /opt/lpanel/current/lpanel manage access --host <域名或IP> --port <端口> --login-path <路径> --https <on或off>` | 应用指定的访问设置，选项可以单独或组合使用 |

`--host auto` 恢复服务器地址访问，`--login-path /` 恢复根路径。启用 HTTPS 时如果当前为自动地址模式，要求显式填写要签发证书的公网 IP 或域名。首次使用 Let’s Encrypt 时可用 `--agree-tos` 明确确认条款。命令输出由最终生效配置构造，IPv6 地址在 URL 中带方括号。自动地址模式按安装文档既有的服务器地址识别方式提供登录地址。

## 账号与登录

CLI 打开现有安装目录和 SQLite，通过管理员单行记录更新所选字段。密码沿用初始化的长度规则及 bcrypt，更新密码和使会话失效通过同一 SQLite 事务提交。用户名和密码修改直接作用于当前登录服务。执行权限沿用服务器 root，命令无需调用网页 API。

## 访问设置生效

1. 加载现有面板配置和绑定，将明确指定的字段合并为候选设置。验证端口、登录路径及主机名。IP 仅适用于面板绑定，规范化后用现有绑定和面板设置字段持久化，沿用当前数据库结构。
2. 端口和登录路径变更沿用当前 HTTPS 状态。HTTPS 开启时更换地址，先为目标地址取得匹配证书，再切换访问入口。需要切换到 HTTP 时，可以在同一条命令显式指定 `--https off`。HTTPS 模式沿用现有面板与 HTTP-01 验证端口 80 分离的约定。
3. 复用 Nginx 生成、语法检查、重载和本机生效标识探针。候选跳转地址取候选端口，IPv6 的监听和 Host 处理使用正确的地址格式。IP HTTPS 握手不能依赖 SNI，让相应监听的默认 TLS 证书覆盖面板 IP，并验证同端口的域名站点仍按各自的 SNI 提供证书。 [Nginx HTTPS 配置](https://nginx.org/en/docs/http/configuring_https_servers.html#sni)
4. 候选 Nginx 配置生效后保存配置，通过 systemd 重启面板服务使入口和 HTTPS 会话规则生效。从本机按新协议、Host 和入口检查登录页，再输出完整地址。面板重启可能短暂中断访问，并会中断正在运行的面板任务，文档说明在空闲时操作。
5. 签发失败时继续使用原证书和入口。Nginx 检查、应用、面板重启或入口验证失败时，利用操作前的面板配置及绑定恢复旧入口，并报告失败阶段和恢复结果。

网页已有的登录地址复制功能使用同一个配置字段。对 IPv6 补齐方括号，确保它与 CLI 生成的链接一致。

## 自动证书

- 域名使用现有 Certbot 流程，优先复用当前地址有效的证书和配置的证书账户。没有账户时，可由 CLI 在确认条款后创建 Let’s Encrypt 账户。
- 公网 IPv4 和 IPv6 由 LPanel 使用项目已依赖的 `golang.org/x/crypto/acme` v0.58.0 直接发起 ACME 订单，使用 IP 标识和 Let’s Encrypt 的 `shortlived` 配置。以 IP 作为证书请求的 SAN，由 Let’s Encrypt 签发有效期为 160 小时的证书。使用内置 Go 客户端可在现有支持系统上提供 IP 证书能力，无需增加系统软件依赖。 [Go ACME 客户端](https://pkg.go.dev/golang.org/x/crypto@v0.58.0/acme#WithOrderIssuanceProfile) · [Let’s Encrypt 短期证书](https://letsencrypt.org/docs/profiles/#shortlived)
- 面板在已有 Nginx 验证目录写入 HTTP-01 验证文件，由 Let’s Encrypt 从公网 80 端口读取，验证结束后清理文件。为尚未监听的 IPv6 准备验证路由时，先在 80 端口提供验证文件并保留此前的面板入口，申请失败时恢复原配置。 [HTTP-01 验证](https://letsencrypt.org/docs/challenge-types/#http-01-challenge)
- 复用已同意条款的 Let’s Encrypt 账户配置，或在管理员确认条款后建立配置。Go 客户端的 ACME 账户密钥单独保存在该账户的私有数据目录，证书私钥与证书链沿用面板既有的证书目录契约。启用前检查有效期和地址匹配，待 Nginx 生效后确认安装成功。
- 复用每天两次运行的 `lpanel-renew.timer`。IP 证书进入续期窗口后，由内置客户端重新申请、加载证书并更新到期时间。续期失败时保留原证书，留待下次定时检查重试。关闭 HTTPS 时停止该面板入口的自动续期，保留已签发的证书供重开时复用。
