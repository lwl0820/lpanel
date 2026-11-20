# ACME 提供商与账户注册设计

## 界面

`web/src/views/AcmeAccountsView.vue` 复用项目现有的 `el-select` 和 `el-option`，选项来自账户列表接口。保留可见的表单标签和用户主动提交。选中 ZeroSSL 时默认显示邮箱注册，并允许展开手动凭据。选中 Google Cloud Public CA 时直接显示两项必填凭据及官方获取说明。编辑待配置账户时采用对应提供商的同一套表单逻辑。

## 注册

在 `internal/panel/acme.go` 的提供商列表中加入标识 `google`、名称和条款链接，使用正式目录 `https://dv.acme-v02.api.pki.goog/directory`。Google 签发免费，注册密钥来自用户有权限的 Google Cloud 项目。凭据在 7 天内使用，每对只能注册一个账号。Google 支持现有的 HTTP 域名验证。 [Google 注册指南](https://docs.cloud.google.com/certificate-manager/docs/public-ca-tutorial) · [HTTP 验证说明](https://docs.cloud.google.com/certificate-manager/docs/public-ca#public-ca-challenges) · [Google Trust Services 条款](https://pki.goog/repository/)

ZeroSSL 在用户同意条款、邮箱有效且两项绑定凭据均未填写时，通过固定 HTTPS 地址 `https://api.zerossl.com/acme/eab-credentials-email` 请求凭据。请求使用表单编码、请求上下文和明确超时。已有完整凭据时直接使用。校验返回状态及两项凭据，失败时仅返回可操作的中文提示。1Panel 的注册代码同样使用邮箱获取绑定凭据，再通过 EAB 完成注册。ZeroSSL 的公开文档另有基于 API Access Key 的生成说明。 [1Panel 注册源码](https://github.com/1Panel-dev/1Panel/blob/dev-v2/agent/utils/ssl/acme.go#L133-L158) · [1Panel 邮箱取凭据源码](https://github.com/1Panel-dev/1Panel/blob/dev-v2/agent/utils/ssl/acme.go#L318-L352) · [ZeroSSL ACME 文档](https://zerossl.com/documentation/acme)

两家提供商共用已经存在的 Certbot EAB 注册方法。注册失败时沿用 `needs_eab` 状态，新建账户可在原记录上重试。将原先写死 ZeroSSL 的注册错误、待配置错误和前端提示改为对应提供商的提示。凭据沿用权限为 `0600` 的临时 Certbot 配置，用完删除。

## 兼容

复用现有账户表和请求字段，按现有提供商标识判断可用的注册方式，无需迁移数据库。签发使用所选账户及其服务地址。续期读取原账户目录中 Certbot 保存的签发配置，不重复获取注册凭据。安装脚本和 CLI 继续沿用既有入口和参数约定。 [现有账户与续期契约](../../../../spec/backend/hosting-contract.md)
