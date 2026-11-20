# 面板命令行管理工具实施计划

## 实施顺序

1. 在 `cmd/lpanel` 增加 `manage` 子命令，接入现有安装目录，处理帮助、账号操作和访问选项。在 `internal/panel/auth.go` 复用管理员密码规则和存储方式，验证旧凭据及旧会话失效。
2. 在面板模块实现访问设置合并、校验、候选配置生成及失败恢复。沿用现有普通域名规则，面板绑定额外识别 IPv4 和 IPv6。核对候选端口、IP 默认 TLS 监听、Nginx 真实生效、面板重启和登录入口。
3. 用项目现有的 Go ACME 依赖实现 Let’s Encrypt IP 申请和续期。使用 IP 标识、`shortlived` 配置和 HTTP-01，复用验证目录、证书目录及续期定时器。覆盖账户与条款、首次申请、切换地址、关闭及重开 HTTPS、续期和申请失败保留原入口。
4. 在 `web/src/domain-api.ts` 核对现有登录地址输出，支持 IPv6 方括号。更新安装文档，列出命令、升级后的入口、签发前置条件以及变更期间的短暂重启。

## 验证

- 按仓库说明在工作目录 `.tmp` 设置 Go 缓存和临时目录。执行 `go test ./cmd/lpanel`，以及 `go test ./internal/panel -run 'Test(Management|AuthenticationLifecycle|AdministratorPersistence|SecureAndExpiredSession|LoginPath|Binding|Certificate|ACME|NginxCommit|ReloadSignal)'`。新增用例集中覆盖旧凭据失效、端口重载失败、候选入口、IP 证书调用与续期。
- IPv6 登录地址复用前端现有的类型检查及相关测试。通过 `npm --prefix web run typecheck` 验证类型，使用已有登录地址测试或最小定向用例检查输出。
- 在隔离环境通过现有发行包入口运行管理命令，验证真实 Nginx 生效、系统服务重启后的持久化、IPv4 和 IPv6 的证书匹配及失败恢复。用测试 CA 或 ACME 协议替身检查 IP 订单、短期证书配置、HTTP-01、续期及低版本 Certbot 环境。记录协议测试、本机 TLS 和真实公网签发各自的验证结果。
- 在具备公网 IPv4 或 IPv6 及可达 80 端口的目标环境，分别验收 Let’s Encrypt 签发、浏览器信任和定时续期。缺少外部条件的项目按仓库规范记入验收记录。

## 审核重点

- 检查 Nginx 与数据库的回退，尤其是从已启用 HTTPS 的地址切换到新地址、端口被占用、证书申请失败和重启后入口无效时。
- 确认旧安装使用已有配置与数据获得新命令，面板与业务站点同时在常用端口正确使用各自证书，IP 签发失败不影响普通域名签发。
- 审核需求、设计、实施计划以及实现和检查所需的规范清单后，再启动 Trellis 实施阶段。
