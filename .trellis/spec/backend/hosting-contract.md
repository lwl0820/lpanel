# 网站、项目、证书与升级契约

## 1. 范围与触发

来自 internal/panel 的 domains.go、projects.go、sites.go、upgrades.go、upgrade_worker.go，以及 deploy/install.sh 和 scripts/build-release.sh。修改网站入口、Compose、证书、发行格式或升级流程时必须同步核对这些边界。

## 2. 签名

- RegisterProjectRoutes、RegisterSiteRoutes、RegisterUpgradeRoutes 在 Open 中创建表并注册接口；域名由 registerDomains 接入。
- SaveBinding(ctx, Binding)、DeleteBinding(ctx, id)、ApplyNginx(ctx)、RequestCertificate(ctx, id)、RenewCertificates(ctx) 是项目、网站和面板实际复用的边界。
- SaveSite(ctx, Site)、DeleteSite(ctx, id) 同步网站记录和关联绑定。
- UpgradeWorker(ctx, root, version, operationID, releaseDir) 在独立 systemd service 中执行版本切换。
- CLI：setup-nginx --root；bind-panel --root --domain [--auto-cert --agree-tos --email --acme-server]；renew --root；upgrade-worker 由内部升级流程调用。

## 3. 数据与环境契约

Binding 包含 id、kind(panel/project/site)、resource_id、domain、upstream、directory、spa、service、container_port、auto_certificate、certificate_status、certificate_error、expires_at。域名全局唯一；项目使用真实发布端口生成 upstream，网站使用 directory/spa，面板使用安装配置端口。

Compose config 输出的 published 是字符串，可能缺失或为动态端口/范围；expose 是含协议/范围的字符串数组。ps 返回 JSON Lines 或空内容，项目代码也兼容数组。仅 expose 不生成不可达上游，UDP 不作为网站代理。项目列表不返回环境变量秘密，编辑详情才返回配置。

Nginx 文件为配置目录下 lpanel.conf；每次配置的面板精确路径 /.lpanel/config 返回本次标识。重载后用候选 HTTP/HTTPS 地址、Host/SNI 从本机回读匹配才提交；NginxProbe 是测试可替换的实际运行边界。仅本机探针接受测试 CA，不影响用户访问的证书验证；公开验证文件为 Root/acme-webroot/.well-known/acme-challenge，私钥为 Root/data/acme。申请和续期必须使用同一组 config/work/log 目录。Certbot 仅申请和续期，不自动改写站点。

发布标签为 vX.Y.Z，附件 lpanel_vX.Y.Z_linux_ARCH.tar.gz 及同名 .sha256。包内平铺 lpanel、web、deploy。程序和前端一起切换 current；data 不随发行包覆盖。公开发布源保存在 upgrade_settings，初始值来自 Config.ReleaseRepository。

## 4. 校验与错误矩阵

| 场景 | 规定结果 |
| --- | --- |
| Compose 校验失败或 Docker 不可用 | 显示实际错误，不把保存配置当成部署成功 |
| 网站目录无效 | 拒绝保存，不递归修改用户目录权限 |
| Nginx 检查或生效失败 | nginx -t/reload 返回 0 也须回读标识；失败以独立超时上下文恢复原文件、重载及绑定状态，不能误报证书已启用 |
| 首次签发失败 | 失败原因可见，保留 HTTP；续期失败则保留已有 HTTPS |
| 不同进程打开数据库 | CLI Open 不重置主服务运行中操作，只有 Serve 启动恢复中断状态 |
| 缺少发行附件 | 保持不可升级，不因版本号较新而重新显示升级按钮 |
| 新版启动失败 | 恢复 previous current，并重启上一版 |
| 安装时域名或证书失败 | 最终地址使用服务器 IP 的 HTTP 入口，不输出无法解析域名的假可用地址 |

## 5. 正常、基础与失败案例

正常：配置扫描→部署→读取真实端口→绑定→证书；网站编辑同步已有域名配置；系统独立 worker 升级并保存结果。
基础：无发布源显示未配置，无域名可用 IP:端口管理，无证书不阻止 HTTP 网站使用。
失败：无效 Nginx 配置回退旧文件；网站保存/删除失败恢复旧记录；删除项目不使用 --volumes，删除网站不删除内容目录。

## 6. 必需测试

项目测试覆盖短/长端口、变量、expose、JSONL、实际端口更新和保留卷；LPANEL_DOCKER_INTEGRATION=1 开启真实 Docker 测试。域名测试覆盖配置标识匹配、重载 0 但配置未生效、探针超时、原 HTTP/HTTPS 保留、Host/SNI 和 CLI Open 不打断服务任务；网站测试覆盖目录/绑定同步、失败恢复和内容保留。升级测试覆盖语义版本、附件、校验下载、成功/失败回退及鉴权。tests/install-smoke.sh 在一次性 Debian 容器中验证安装分支，系统服务和包管理使用替身，不能等同真实服务器验收。

## 7. 错误与正确用法

错误：让证书续期 CLI 调用 Open 时把全部 running 操作置为 failed；把 ps JSON Lines 直接当整体数组解析；仅因版本号较新就认为有可用发行包。
正确：仅 Serve 启动时恢复普通中断操作；按真实 CLI 输出解析；版本、附件和检查结果共同决定是否可升级。CLI 绑定面板 HTTPS 后重启主服务，确保 Secure Cookie 与磁盘配置一致。
