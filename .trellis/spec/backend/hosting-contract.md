# 网站、项目、证书与升级契约

## 1. 范围与触发

来自 internal/panel 的 domains.go、projects.go、sites.go、upgrades.go、upgrade_worker.go，以及 deploy/install.sh、deploy/uninstall.sh、scripts/build-release.sh 和 .github/workflows/release.yml。修改网站入口、Compose、证书、发行格式、安装、升级或卸载流程时必须同步核对这些边界。

## 2. 签名

- 宿主机程序由 `/api/v1/programs` 管理。新建和复制均生成独立 ID 与 `Root/programs/<id>` 目录，初始停止。启动设置保存在 SQLite，启动和重启时应用到对应的 Supervisor 配置。保存入口时要求目录内的普通文件，并为其设置执行权限。
- 复制返回 `program` 与 `skipped_links`，跳过符号链接。删除程序时移除该程序的绑定、托管配置和日志，显式传入 `remove_files=true` 时才删除业务目录。复制和清理均检查目录内的宿主机挂载点。
- 程序域名绑定使用 `kind=program` 和程序 ID。新选的上游必须与主进程当前监听的 TCP 端点匹配。程序停止后可保留原端点修改证书设置或协议。

- RegisterProjectRoutes、RegisterSiteRoutes、RegisterUpgradeRoutes 在 Open 中创建表并注册接口；域名由 registerDomains 接入。
- SaveBinding(ctx, Binding)、DeleteBinding(ctx, id)、ApplyNginx(ctx)、RequestCertificate(ctx, id)、RenewCertificates(ctx) 是项目、网站和面板复用的边界。需指定账户时使用 SaveBindingWithAccount 和 RequestCertificateWithAccount。
- SaveSite(ctx, Site)、DeleteSite(ctx, id) 同步网站记录和关联绑定。
- `POST /api/v1/projects` 和 `PUT /api/v1/projects/{id}` 接收 `name`、`compose`、`environment` 和可选的整数数组 `custom_ports`。每项取值 1 至 65535，重复项保留首次出现的位置。旧项目初始列表为空，更新时省略该字段保留原值，传入 `[]` 清空。两者通过配置校验并保存后均提交 `project_deploy` 后台操作。响应为 `200`，项目字段在顶层，并附带 `operation`。
- `GET /api/v1/projects/{id}/export` 返回 ZIP 附件，`POST /api/v1/projects/import` 接收 `application/zip` 原始请求体。导出按登记目录读取，导入在目标面板建立新的本地项目记录，响应和自动部署流程与新建项目一致。同名项目或目录返回 409。配置校验失败时清理本次导入目录。ZIP 的 `project.json` 记录显示名及可选的 `custom_ports` 数组，旧包导入时端口列表为空。`files/` 保存项目目录，导入恢复普通权限位和数值 UID/GID。不能用作本地目录名的旧项目名称沿用 ID 目录机制。
- 新版服务启动时为已有的 LPanel 托管 Nginx 配置补齐导入代理规则，已包含该规则时跳过重载。刷新失败时记录日志并继续启动。
- 项目响应的 `directory` 是登记目录的绝对路径。`GET /api/v1/projects/{id}/status` 返回容器列表，含 Docker 的 `ID`。`GET /api/v1/projects/{id}/containers/{containerID}/logs` 查询项目中的指定容器，返回 `{"log":"…"}`。项目汇总日志仍可通过 `GET /api/v1/projects/{id}/logs` 获取。
- `POST /api/v1/projects/{id}/containers/{containerID}/actions/{action}` 接受 `start`、`stop`。以项目状态列表中的容器 ID 定位副本，成功返回 `200` 和 `message`。无效动作返回 `400`，容器不属于项目时返回 `404`，Docker 执行失败时返回 `502` 并按项目环境变量脱敏。
- `GET /api/v1/images` 按完整镜像 ID 合并标签，返回数组，字段为 `id`、`tags`、`size`。无标签时返回空数组，大小沿用 Docker 格式化字符串。`DELETE /api/v1/images/{id}` 普通删除单个镜像，保留其未打标签的父镜像。引用或多个标签造成的冲突会返回 Docker 原因。`POST /api/v1/images/prune` 清理没有任何容器引用的镜像，包括带标签镜像，保留已停止容器的镜像。两个写接口均在 Docker 完成后返回 `200` 和 `message`。
- `GET /api/v1/projects/{id}/runtime-ports` 返回 `ports`、`running` 及可选的 `warning`。已发布 TCP 候选按服务名与容器端口表示，使用当前实际的宿主机端点。host 候选按服务名及监听地址、端口表示，只纳入当前项目容器进程持有的 TCP 监听套接字。某个服务的监听信息读取失败时，结果中保留已发现的可用端口。
- `POST /api/v1/projects/{id}/bindings` 选择已发布端口时传 `service` 和 `container_port`，部署、启动或重启后按 Docker 当前的发布映射刷新上游。选择已保存的自定义端口时传 `host_port`，上游固定为所选 HTTP/HTTPS 协议下的 `127.0.0.1:<端口>`。选择 host 网络监听时传 `listen_target` 对象，包含 `service`、`host_ip`、`host_port`。保存前重新核对该服务当前监听的端点，绑定记录保存服务名、宿主机端口及固定上游地址。混用目标、提交过期监听或提交未列入该项目候选的手动端口时返回 400。移除候选后，现有绑定继续使用已保存的上游。
- `PUT /api/v1/projects/{id}/bindings/{bindingID}` 原地更新该项目的现有绑定。传入域名、`scheme` 和证书设置；省略目标字段时按库内当前上游保留主机、端口和目标类型，新选目标使用创建接口的三类字段与校验。切换到固定目标后保留固定上游，切换到发布映射后按服务名和容器端口跟随重部署。域名不变且证书已启用时，沿用签发账户和到期时间。代理配置应用失败时恢复之前的绑定与配置。
- `POST /api/v1/projects/{id}/actions/upgrade-images` 返回 `202` 和 `project_upgrade-images` 后台操作。按当前 Compose 配置的镜像引用与平台向仓库拉取，所有拉取成功后才更新使用旧镜像的运行中服务。已停止或尚未启动的服务只拉取镜像。升级完成时，按运行中服务的实际发布端口校正动态域名上游。若上次重建成功但绑定刷新失败，重试可在保留新容器的情况下恢复转发。
- `POST /api/v1/projects/{id}/actions/delete` 返回 `202` 和后台操作。带 `?remove_files=true` 时同时清理该项目的目录，省略该参数时只移除目录根部的 `compose.yaml` 和 `.env`。
- `POST /api/v1/sites` 接收 `hostnames` 字符串数组、`identifier`、`note`、`directory`、`spa` 和可选的 `auto_certificate`、`acme_account_id`。成功返回 `201`、顶层网站字段、`bindings` 数组及可选的 `operation` 或 `certificate_error`。`PUT /api/v1/sites/{id}` 保存 `identifier`、`note`、`directory` 和 `spa`。单独绑定域名与手动申请入口继续接受可选 `acme_account_id`。
- `GET /api/v1/acme/accounts` 返回 `accounts`、`providers` 和 `default_account_id`。在该路径通过 POST 新增账户，通过 PUT 或 DELETE 操作 `/{id}` 下的账户。输入使用 `name`、`provider`、`email`、`agree_tos` 和可选的 `eab_kid`、`eab_hmac_key`。账户响应包含 `provider_name` 与 `needs_eab`。
- UpgradeWorker(ctx, root, version, operationID, releaseDir) 在独立 systemd service 中执行版本切换。
- CLI 支持 `setup-nginx --root`、`bind-panel --root --domain`、`renew --root`。绑定时可用 `--auto-cert`、`--agree-tos`、`--email`、`--acme-provider`、`--acme-eab-file` 和兼容的 `--acme-server`。内部升级调用 `upgrade-worker`。
- 安装入口 `sudo bash deploy/install.sh [选项]` 支持 `--repository`、`--archive`、`--username`、`--password`、`--port`、`--login-path`、`--domain`、`--auto-cert`、`--agree-tos`、`--email`、`--acme-provider` 和 `--acme-eab-file`。 [参数详情](../../../docs/install.md#命令行参数)
- 官方安装布局的卸载入口为 `sudo bash deploy/uninstall.sh`。脚本从终端询问是否清理 Compose 项目，并单独读取最终确认。

程序的 `GET /api/v1/programs` 返回 `programs` 和 `dependency_error`。Supervisor 不可用时仍可使用面板的其他功能。详情的 `/runtime` 读取主进程的 PID、CPU 百分比、常驻内存字节数、TCP 连接数及当前监听地址端口。缺少前一次 CPU 读数时返回 null。

管理员可通过 `POST /api/v1/programs/supervisor/install` 按需安装并启用 Supervisor。接口返回 HTTP 202 和操作记录，重复提交可继续跟踪进行中的安装。缺少 supervisorctl 时通过 apt 安装软件包，已有客户端时直接启用服务，以 supervisorctl 能读取服务 PID 作为成功条件。

`GET /api/v1/programs/{id}/logs/stream` 使用管理员会话和 SSE。首次发送 `snapshot`，随后发送 `append`，两者的数据均为 JSON 字符串。日志轮转或截短时以新快照替换旧内容。Nginx 关闭此路由的响应缓冲，升级时已有面板入口也会补齐对应规则。浏览器切换程序或离开日志时应结束旧连接。

## 3. 数据与环境契约

Binding 包含 id、kind(panel/project/site/program)、resource_id、domain、upstream、directory、spa、service、container_port、host_port、auto_certificate、certificate_status、certificate_error、expires_at 和实际签发账户 `certificate_account_id`。域名全局唯一。项目绑定记录由 service 和 container_port 标识动态发布的端口，或由 host_port 标识固定的宿主机端口。网站使用 directory/spa，面板使用安装配置端口。

编排项目的代理位置用 `$remote_addr` 覆盖 `X-Real-IP` 和 `X-Forwarded-For`，上游收到的两个头均为单个地址。所有项目共享 HTTP 层的升级映射。请求带非空 `Upgrade` 时保留协议并向上游发送 `Connection: upgrade`，普通请求使用 `Connection: close`。配置首行保留托管标记，供升级和卸载识别。

证书账户从 SQLite 按加入顺序读取，最早的现有账户为默认。无账户时 `default_account_id` 为 `null`。面板提供商标识为 `letsencrypt`、`zerossl` 和 `google`，另支持旧自定义地址对应的兼容值。新账户用独立的 `Root/data/acme-accounts/<ID>`，已迁移旧账户用原 `Root/data/acme`。旧证书原地保留。签发使用所选账户，定时续期按绑定实际账户和域名读取 Certbot 保存的签发配置。

ZeroSSL 和 Google Cloud Public CA 在注册时通过 EAB 绑定账户。ZeroSSL 未提交两项凭据时，在用户同意条款后使用联系邮箱向 `https://api.zerossl.com/acme/eab-credentials-email` 获取凭据。有完整凭据时直接注册。Google Cloud Public CA 要求提交两项凭据，使用正式地址 `https://dv.acme-v02.api.pki.goog/directory`。绑定凭据通过权限为 `0600` 的临时配置交给 Certbot，注册结束后清理。 [Google 凭据获取指南](https://docs.cloud.google.com/certificate-manager/docs/public-ca-tutorial)

新建网站至少有一个主机名。主机名统一小写，忽略首尾空白、尾随点和空行。留空的 `identifier` 取第一个主机名。标识独立于内部 ID，数据库沿用 `sites.name` 保存并建立唯一索引，新增 `note` 列。旧数据中重复的名称获得唯一后缀，目录与绑定继续按原记录使用。

新建网站的 `directory` 留空时，在 `Root/sites/` 下按标识分配可供 Nginx 遍历的网页目录。目录名中的不安全字符替换为 `_`。例如 `*.example.com` 对应 `_.example.com`。手动指定时沿用已存在的绝对目录及其权限。编辑网站时传回已保存或用户手动指定的目录。

静态网站支持 `*.example.com` 形式的通配符 HTTP 绑定。普通域名可以和通配符同时绑定并自动申请 HTTPS。自动签发由一个后台操作逐个处理普通域名，每个域名独立保存签发结果。

Compose config 输出的 published 是字符串，可能缺失或为动态端口/范围；expose 是含协议/范围的字符串数组。ps 返回 JSON Lines 或空内容，项目代码也兼容数组。仅 expose 不生成不可达上游，UDP 不作为网站代理。项目列表不返回环境变量秘密，编辑详情才返回配置。单容器日志依照项目容器列表校验 ID，最多读取最近 200 行，按现有规则脱敏。

新项目名称直接作为目录名，已有项目升级后仍用 ID 目录。新建时可复用未登记的同名目录。Compose 配置留空时从该目录读取已有配置和环境变量。勾选删除文件时，清理范围为登记的项目目录，包括该目录内的 `compose.yaml`、`.env` 和相对路径挂载数据。Docker 命名卷、镜像及目录外的数据保留。

Nginx 文件为配置目录下 lpanel.conf；每次配置的面板精确路径 /.lpanel/config 返回本次标识。重载后用候选 HTTP/HTTPS 地址、Host/SNI 从本机回读匹配才提交；NginxProbe 是测试可替换的实际运行边界。仅本机探针接受测试 CA，不影响用户访问的证书验证；公开验证文件为 Root/acme-webroot/.well-known/acme-challenge，私钥为 Root/data/acme。申请和续期必须使用同一组 config/work/log 目录。Certbot 仅申请和续期，不自动改写站点。

发布标签为 vX.Y.Z，附件 lpanel_vX.Y.Z_linux_ARCH.tar.gz 及同名 .sha256。包内平铺 lpanel、web、deploy。程序和前端一起切换 current；data 不随发行包覆盖。公开发布源保存在 upgrade_settings，初始值来自 Config.ReleaseRepository。

面板服务启动时检查版本，此后每 24 小时检查一次。自动更新设置持久保存，新旧安装均默认关闭。只有后台检查成功并发现可用发行包且开关已开启时，才自动发起现有升级流程。切换设置与手动检查均不触发安装。

推送正式标签会触发 `.github/workflows/release.yml`。从 `main` 手动触发时输入已有的 `tag`。两种路径均检出标签源码、执行 `scripts/build-release.sh`、校验 amd64 和 arm64 发行包，再以四个附件发布正式 Release。安装和升级通过 GitHub Release 条目读取发行包。

在线安装按命令行仓库、`LPANEL_REPOSITORY`、交互输入的顺序取值。安装仅询问当前方式适用的缺项。填写域名后，自动申请证书的交互默认值为 `y`。申请证书时，提供商默认 Let’s Encrypt，所选条款默认同意，联系邮箱默认为 `acme@<转为小写的面板域名>`。按回车接受默认值。申请证书的安装创建首个 ACME 账户，HTTP 安装从空账户列表开始。显式空域名表示使用服务器地址，显式空邮箱表示留空。密码始终通过标准输入交给面板初始化。 [密码传递约定](./logging-guidelines.md)

安装前通过 `init --help` 核对发行包是否支持用户指定的非空 `--login-path`，失败时可更换发行包重试。省略、显式为空或填 `/` 时沿用旧版初始化参数。安装完成前，默认通过 Nginx 探测 `/api/v1/health`，启用入口时直接探测 Go 回环端口。两种模式都确认返回的 `status` 为 `ok` 且 `version` 与发行标签一致，再经 Nginx 检查实际登录地址返回 HTTP 200。域名入口保留域名和协议并在本机连接，证书失败时检查 HTTP 登录地址。探测连接失败时静默重试，持续失败时退出并提示查看服务日志。 [安装脚本](../../../deploy/install.sh)

入口校验仅影响面板虚拟主机。启用时，面板主监听和浏览器使用的辅助 HTTP 跳转监听都交给 Go 校验路径和会话，HTTP-01 仍由 `/.well-known/acme-challenge/` 直接提供。配置标识 `/.lpanel/config` 的探针从 IPv4 回环端口发起。候选路径沿用 Nginx 配置检查、重载和回读流程，再保存配置。常规模式下 Nginx 返回的面板 `index.html` 带 `Cache-Control: no-cache`，浏览器在稍后启用入口时会重新请求页面。

卸载处理 `/opt/lpanel/current`、`/opt/lpanel/releases`、三个 LPanel systemd 单元和带 LPanel 管理标记的 `/etc/nginx/conf.d/lpanel.conf`。移除该配置停用面板及其管理的业务域名入口。默认保留项目容器、目录和记录。选中 Compose 清理时，按 SQLite 记录定位目录（升级前的库按 ID 定位），使用 `lpanel-<ID>`、项目目录内的 `compose.yaml` 和 `.env` 执行 `down --remove-orphans`。Docker 清理成功后移除该项目目录（含相对挂载目录）、项目记录及 `bindings.payload` 中同一项目的绑定。保留其他数据库记录、证书、Docker 卷、镜像和目录外的数据。

## 4. 校验与错误矩阵

| 场景 | 规定结果 |
| --- | --- |
| 目录名非法或名称、路径已登记 | 非法名称返回 400，占用返回 409；同名未登记目录可以用于新建 |
| Compose 校验失败或 Docker 不可用 | 显示实际错误，不把保存配置当成部署成功 |
| 项目已保存，部署任务无法启动 | 返回 `200`、项目字段和 `deployment_error`，可点击「编辑」并重新保存以重试 |
| 后台部署执行失败 | 操作状态为 `failed`，保留项目记录与配置 |
| 查不到所选容器或容器不属于项目 | 单容器日志返回 `404` |
| 项目目录或父数据目录为符号链接，或目录内有宿主机挂载点 | 勾选文件清理的删除操作失败，并提示具体位置 |
| Docker 项目清理、解除域名绑定或文件清理失败 | 后台操作标记失败。Compose 或绑定清理失败时保留项目文件，文件清理失败时保留项目记录 |
| 网站目录无效 | 拒绝保存，不递归修改用户目录权限 |
| 网站标识已存在 | 提示更换网站标识 |
| 默认目录已存在 | 提示修改标识或指定已有的网页目录，保留目录中的数据 |
| 建站时主机名无效、被占用或配置无法生效 | 整批失败，回收本次新建记录、绑定与仍为空的自动目录，自定义目录及旧绑定保留 |
| 建站的 HTTP 绑定已生效，证书任务无法排入 | 返回 `201`、网站和绑定，并带 `certificate_error`，可从域名管理重试 |
| 通配符绑定申请证书 | 拒绝申请，继续提供 HTTP |
| Nginx 检查或生效失败 | nginx -t/reload 返回 0 也须回读标识；失败以独立超时上下文恢复原文件、重载及绑定状态，不能误报证书已启用 |
| 首次签发失败 | 失败原因可见，保留 HTTP；续期失败则保留已有 HTTPS |
| 注册前未同意绑定所需条款、提交不完整的绑定凭据，或 ZeroSSL 自动注册缺少邮箱 | 返回 `400`，提示补全后再提交 |
| 已保存的新账户未能获取绑定凭据或完成注册 | 返回 `201` 和 `needs_eab: true`，通过 PUT 在同一账户上重试 |
| 编辑待配置账户并再次注册失败 | 返回 `400` 和可操作的错误说明，账户仍为待配置 |
| 删除账户时还有在用或仍保留的证书 | 返回 `409`，保留账户和默认状态 |
| 请求签发时无可用账户 | 提示先添加或补全证书账户 |
| 不同进程打开数据库 | CLI Open 不重置主服务运行中操作，只有 Serve 启动恢复中断状态 |
| 缺少发行附件 | 保持不可升级，不因版本号较新而重新显示升级按钮 |
| 新版启动失败 | 恢复 previous current，并重启上一版 |
| 在终端安装时省略选项或只写选项名不填值 | 按当前安装方式补问缺项，不覆盖已提供的值 |
| 非终端执行时只写选项名，或安装值无效、输入提前结束 | 安装写入前退出，提示对应选项或配置 |
| 安装时域名或证书失败 | 最终地址使用服务器 IP 的 HTTP 入口，不输出无法解析域名的假可用地址 |
| 卸载取消、无法确认服务或配置归属、项目目录含实际挂载点 | 保留资源并输出提示 |
| 卸载时 Compose 清理失败或 Nginx 配置检查、重载失败 | Compose 失败保留该项目目录和记录。Nginx 失败时恢复配置文件。两者均保留面板程序，便于处理后重试 |

## 5. 正常、基础与失败案例

正常：配置扫描→部署→读取真实端口→绑定→证书；网站编辑同步已有域名配置；系统独立 worker 升级并保存结果。
新建项目与保存配置均自动提交部署。操作状态为 `success` 后才提示部署完成。
基础：无发布源显示未配置，无域名可用 IP:端口管理，无证书不阻止 HTTP 网站使用。
建站时按主机名建立 HTTP 入口。勾选证书后逐个申请普通域名的证书，个别签发失败后其他普通域名仍继续申请。编辑仅修改备注或标识时无需重载 Nginx。
安装：完整参数支持关闭标准输入。提供账号和端口时，脚本仍会按需询问密码与域名。选择申请证书但域名为空时在安装前报错。
账户：无证书账户可删除。删除默认账户后选最早添加的剩余账户，删空后等待下一次添加。手动改选账户签发成功后，当前证书切换到新账户；失败时继续使用原证书。
失败：无效 Nginx 配置回退旧文件；网站保存/删除失败恢复旧记录；删除网站不删除内容目录。

删除项目沿用 `down --remove-orphans`，保留命名卷。勾选删除文件时，先确认目录边界，然后移除 Docker 资源和绑定，最后清理目录与项目记录。
卸载：默认路径可在保留业务容器的情况下停用面板。清理选项在最终确认前列出项目和目录，重复执行已完成的卸载时提示无需操作。

## 6. 必需测试

项目测试覆盖短/长端口、变量、expose、JSONL、实际端口更新和保留卷；LPANEL_DOCKER_INTEGRATION=1 开启真实 Docker 测试。域名测试覆盖配置标识匹配、重载 0 但配置未生效、探针超时、原 HTTP/HTTPS 保留、Host/SNI 和 CLI Open 不打断服务任务；网站测试覆盖目录/绑定同步、失败恢复和内容保留。升级测试覆盖语义版本、附件、校验下载、成功/失败回退及鉴权。tests/install-smoke.sh 在一次性 Debian 容器中验证安装分支，系统服务和包管理使用替身，不能等同真实服务器验收。
项目测试验证新建和更新均返回可查询的部署操作，部署使用刚保存的配置，校验失败不启动部署。

编排镜像升级须覆盖再次拉取同一标签、仓库拉取失败、已拉取新镜像但容器仍运行旧镜像，以及同项目内运行和停止服务并存。断言重建的服务名、未变化服务的容器 ID 和绑定后的实际端口。使用 containerd 镜像存储和多架构索引时，拉取遵守目标平台，比较容器的 `.Image` 和镜像引用未经平台筛选的 `.Id`。目标列表为空时，直接完成操作；没有服务参数的 Compose 启动命令会作用于整个项目。

单容器日志测试覆盖同服务副本隔离、跨项目容器 ID 和日志脱敏。文件清理测试覆盖默认保留、勾选删除、数据边界和 Compose 失败时的记录保留。真实 Docker 用例核对同时清理项目目录后命名卷仍存在。升级用例须核对旧容器 ID 和挂载来源。默认清理后同名重建须验证 `env_file: .env` 与相对路径业务文件。端口扫描从候选环境文件插值；保存时在登记目录校验服务的相对引用。

`bash tests/uninstall-smoke.sh` 在仓库 `.tmp` 中用实际文件和 SQLite 验证取消、默认保留、选中项目清理、失败恢复和重复执行。Docker、Nginx、systemd 使用命令替身。

`internal/panel/sites_test.go` 覆盖旧库重名迁移、标识和默认目录、批量 HTTP 绑定及整批回滚、备注编辑、证书任务启动失败和签发部分失败时的保留行为。Nginx 验收使用具体子域名验证通配符站点访问。

`tests/install-smoke.sh` 核对混合输入、证书交互默认值、无人值守安装、必填项缺失、显式空值及密码经标准输入交给初始化的行为。健康检查用例核对自定义端口、首次失败后静默重试、HTTPS 域名解析和证书失败后的 HTTP 入口。

证书账户测试覆盖空列表与默认切换、账户仍有在用或保留证书时拒绝删除，以及旧证书升级后继续沿用原目录。用替身测试 ZeroSSL 邮箱注册、失败后手动重试，以及 Google Cloud Public CA 的注册地址、签发和按实际账户续期。安装冒烟验证默认 Let’s Encrypt 和显式 ZeroSSL。

## 7. 错误与正确用法

错误：让证书续期 CLI 调用 Open 时把全部 running 操作置为 failed；把 ps JSON Lines 直接当整体数组解析；仅因版本号较新就认为有可用发行包。
正确：仅 Serve 启动时恢复普通中断操作；按真实 CLI 输出解析；版本、附件和检查结果共同决定是否可升级。CLI 绑定面板 HTTPS 后重启主服务，确保 Secure Cookie 与磁盘配置一致。

创建和更新接口在配置文件与项目记录保存后发起部署。页面收到操作记录后开始轮询，可通过重新保存已校验的配置重试部署。

卸载清理以登记项目为单位，仅在 `down --remove-orphans` 成功后清理目录和记录。全局 Docker 清理或按项目名前缀批量删除会影响其他业务资源。

补发已有标签使用 `gh workflow run release.yml --ref main -f tag=vX.Y.Z`。工作流定义从 `main` 读取，发行包仍使用原标签提交构建。

建站默认目录应位于 `Root/sites/`。将内容放在权限为 `0700` 的 `data/` 会阻止 Nginx 读取。一次建站的整组绑定写入后统一应用 Nginx 配置。HTTP 入口确认生效后才能启动后台证书任务，HTTPS 状态来自各域名记录。

安装脚本根据是否提供参数值决定是否交互。`--domain` 未填值时按需询问，`--domain ''` 明确选择服务器地址，不能把空字符串当作缺项再次询问。

账户选择字段 `acme_account_id` 仅用于该次操作。证书成功启用后才更新 `certificate_account_id`，定时续期始终读取实际生效的记录。

注册凭据只传给 Certbot 注册调用。续期复用既有账户，接口响应、日志和命令参数均省略绑定凭据。
