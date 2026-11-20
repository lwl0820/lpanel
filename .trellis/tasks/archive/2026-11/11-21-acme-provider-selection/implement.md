# 实施与验证计划

1. 在 `internal/panel/acme.go` 添加 Google Cloud Public CA，复用注册流程，接通 ZeroSSL 邮箱取凭据，核对新建和编辑待配置账户的校验及失败重试。
2. 在 `web/src/views/AcmeAccountsView.vue` 替换选择控件，接通 ZeroSSL 邮箱与手动两种路径，展示 Google Cloud 凭据说明。只在接口契约确有变动时同步改动 `web/src/acme-api.ts`。
3. 更新证书账户的项目契约。实现和验证重点放在注册分支、待配置状态、密钥传递、签发与原账户续期。

## 验证

- 补充 ZeroSSL 邮箱注册与失败后手动重试的回归验证，并扩展现有注册用例验证 Google 的服务地址、凭据校验和错误文案。外部服务和 Certbot 用替身，避免消耗真实配额或注册真实账户。
- 运行 `go test ./internal/panel -run '^TestACME'`、`npm --prefix web run typecheck`、`npm --prefix web run lint` 和 `npm --prefix web run build`。缓存和临时文件放在工作区 `.tmp`。
- 浏览器核对下拉键盘操作、切换选项后的字段和条款、ZeroSSL 注册方式切换及 Google Cloud 凭据校验。真实签发需要测试域名及其公网 80 端口，Google 还需要对应项目凭据。交付时注明真实签发和续期的验证结果。
