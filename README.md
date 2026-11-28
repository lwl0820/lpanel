# LPanel

使用 Go、Vue 3 和 Element Plus 编写的中文服务器管理面板。Nginx 提供前端和网站访问入口，面板通过 Docker Compose 部署应用，通过 Let’s Encrypt 管理 HTTPS 证书。

## 首版功能

- 单管理员登录，安装时设置账号、密码和访问端口。
- 「服务器管理 → 文件管理」从宿主机根目录浏览文件，支持新建目录、下载、编辑文本、重命名，以及批量删除、复制、剪切和修改权限。删除文件夹会一并删除其中的内容。权限和所有者可在同一弹窗中修改，并可选择应用到子文件和子文件夹。支持在目录间粘贴服务器文件，也可选取、拖入或粘贴本机文件上传到当前目录。上传按分片传输，逐个文件显示进度。可将多个文件或文件夹压缩为 ZIP，压缩包名称可改；解压时可使用当前目录或指定其他目录。
- 「服务器管理 → 防火墙」查看本机 UFW 状态、启停防火墙并维护入站端口规则。启用前自动检查当前面板端口，不自动开放 SSH。
- 「服务器管理 → 交换空间」逐项查看交换文件和只读交换分区，可新建独立文件、调整或停用面板创建的文件，也可删除选中的系统交换文件。
- 「服务器管理 → 时区与时间同步」显示服务器当地时间，支持更换时区、开关自动校时，以及通过 systemd-timesyncd 立即同步一次。
- 「服务器管理 → DNS 设置」可查看和修改整台服务器默认使用的一组 DNS 地址，并恢复首次修改前的设置。
- 首页「服务器资源」显示整机 CPU、内存和系统盘用量。「服务器监控」提供 CPU 型号、核心数、系统平均负载、各磁盘用量和最近 30 天的整机趋势。「服务器管理 → 实时进程」可按 CPU、内存或磁盘读写速率查看进程排名。
- 「程序管理」通过 Supervisor 托管宿主机上的二进制和可执行脚本。左侧选择程序，右侧查看主进程资源、实时日志或修改启动设置。「打开目录」进入现有文件管理，点击监听端口可在现有域名弹窗绑定域名及申请证书。
- Compose 配置与环境变量输入、端口扫描、部署、启停、重启、状态及日志。
- 在「容器管理 → 编排项目」展开项目，可单独启动、停止容器或查看日志。选择项目行的「更多操作 → 升级镜像」会按当前配置强制拉取镜像，并重建镜像有变化的运行中服务。已停止的服务保持停止。切换到「镜像管理」可查看、删除本地镜像，或清理未被任何容器使用的镜像。
- 在「编排项目」导出项目后，可在另一台面板导入 ZIP 压缩包。压缩包包含自定义端口列表，以及项目目录里的配置、隐藏文件（包括 `.env`）和数据。目标面板恢复文件后立即部署。Docker 命名卷、镜像、目录外的挂载数据和面板域名绑定需另行迁移。若目录中有符号链接、实际挂载点或特殊文件，导出会提示处理。
- 为 HTTP/HTTPS 应用绑定域名，支持 WebSocket。在「容器管理 → 编排项目」打开项目行的「更多操作 → 绑定域名」，或从「域名与证书」选择项目。弹窗会列出运行中服务的实际端口映射，以及该项目 host 网络服务正在监听的 TCP 端口。选择端口和上游协议即可绑定。需要手动指定端口时，可在项目编辑页的「服务端口」中添加并保存。已有绑定可在「域名与证书 → 对应域名的更多操作 → 编辑」中更换端口或切换上游协议。同一域名继续使用现有证书。
- 静态网站支持一次绑定多个主机名、设置网站备注、为普通域名自动申请 HTTPS 证书，以及单页应用路由回退。
- 域名旁勾选自动申请证书，签发成功后启用 HTTPS，并自动续期。
- 检查 GitHub Releases 正式版本，支持手动升级和自动更新。升级会应用新版的托管 Nginx 配置，失败时恢复上一版程序。
- 一键安装，支持安装时绑定域名并签发面板证书。

## 运行方式

面板以 Go 二进制运行，由 systemd 管理。Nginx、Docker 和 Compose v2 运行在宿主机，数据使用本地 SQLite 保存。安装根目录为 `/opt/lpanel`，程序和前端按版本放在 `releases/` 中，`current` 指向运行版本，持久化数据放在 `data/`。

新建静态网站时，至少填写一个主机名，每行填写一个。网站标识默认取第一个主机名，可改成其他未使用的标识。网页目录留空时，面板会按标识在 `/opt/lpanel/sites/` 下创建目录，并将不适合用作文件名的字符替换为下划线。例如标识 `*.example.com` 会生成 `_.example.com`。也可以指定已有目录。创建后可在网站列表查看完整路径。

目标系统为 Debian 12/13、Ubuntu 22.04/24.04/26.04 LTS，目标架构为 amd64 和 arm64。实际验证结果以交付记录为准。

面板默认访问端口为 `8443`。未申请证书时使用 `http://服务器地址:端口`；面板域名签发证书成功后使用 `https://面板域名:端口`。业务网站使用标准的 80/443 端口。

## 通过 MCP 连接 Agent

1. 登录「面板设置」，在「MCP 接入」中填写 Agent 名称，签发令牌。复制并保存令牌，明文仅在签发时显示一次。
2. 开启「启用 MCP」，复制连接地址。公网部署请先为面板启用 HTTPS，使用带实际面板端口的 HTTPS 地址。
3. 在支持自定义请求头的 MCP 客户端选择 Streamable HTTP，填入连接地址，并设置请求头 `Authorization: Bearer <令牌>`。连接后刷新工具列表即可使用。

每枚令牌都能管理宿主机文件、容器、静态网站、域名和证书。文件访问范围与面板管理员相同。可以单独撤销某个 Agent 的令牌，也可以关闭 MCP，阻止所有 Agent 的后续请求。已提交的后台操作继续按面板原有流程执行。

## 宿主机程序

首次打开「程序管理」时，如果提示 Supervisor 未就绪，点击「安装并启动 Supervisor」。面板会按需从 Debian 或 Ubuntu 的软件源安装软件包，启动服务并设置开机自启。连接成功后提示自动消失，失败时可查看日志并重试。

新建程序后点击「打开目录」上传文件，回到「启动设置」填写入口相对路径、参数及已有系统用户，再点击「启动」。建议为业务程序使用非特权账户。运行指标和监听端口来自 Supervisor 报告的主进程。浏览器断开后，Supervisor 仍会托管程序。运行中的程序异常退出时会自动拉起。服务器或 Supervisor 服务重启后，需要在面板手动启动。

## 域名与证书前提

将域名解析到服务器，并在防火墙及云安全组开放公网 80 和实际访问端口。普通域名的证书通过 80 端口进行 HTTP-01 验证，面板使用自定义端口时也一样。静态网站的通配符绑定（如 `*.example.com`）提供 HTTP 访问。

首次签发失败时保留 HTTP 入口，并显示原因；修正解析或端口条件后可重试。已有证书续期失败时，保留当前 HTTPS 配置。

## 数据保留

新建 Compose 项目时，项目名称也是 `data/projects/` 下的文件夹名称。已有同名文件夹且未被其他项目使用时，可以复用其中的文件。升级前创建的项目继续使用原目录。

删除 Compose 项目时会移除容器、网络、面板绑定以及项目目录根部的 `compose.yaml` 和 `.env`。默认保留目录及其中的其他文件；同名重建时，重新填写配置即可复用保留的业务数据。选择同时删除整个目录时，其中的相对挂载数据也会删除。Docker 命名卷和项目目录外的文件保留。删除静态网站时保留服务器上的内容目录。升级切换程序与前端版本，保留管理员、项目、网站和证书数据。

优先在干净服务器安装。已有可用 Docker 可以复用；发现非本面板管理的 Nginx 或端口冲突时，安装程序提示并停止相关步骤，不覆盖旧配置，不自动迁移其他面板。

## 开发运行

需要 Go 1.27 或更新版本、Node.js 24/26 与 npm。首次准备依赖和工作目录：

```bash
mkdir -p .tmp/go-cache .tmp/go-mod .tmp/build .tmp/npm-cache
export TMPDIR="$PWD/.tmp/build"
export GOTMPDIR="$TMPDIR"
export GOCACHE="$PWD/.tmp/go-cache"
export GOMODCACHE="$PWD/.tmp/go-mod"
export npm_config_cache="$PWD/.tmp/npm-cache"
go mod download
npm --prefix web ci
go build -o .tmp/lpanel ./cmd/lpanel
```

初始化本地账号，密码通过标准输入读取：

```bash
read -r -s -p '管理员密码：' panel_password
printf '\n'
printf '%s\n' "$panel_password" | .tmp/lpanel init --root "$PWD/.tmp/dev-panel" --username admin
unset panel_password
.tmp/lpanel serve --root "$PWD/.tmp/dev-panel"
```

另开终端运行 `npm --prefix web run dev`，通过 Vite 输出的地址访问。开发代理将 API 转发到本机 `127.0.0.1:18080`。开发模式没有安装宿主机 Nginx、Docker 或 Certbot；涉及系统服务的操作需在隔离的完整环境验证。

## 在线一键安装

在 Debian 12/13 或 Ubuntu 22.04/24.04/26.04 LTS 服务器（amd64 或 arm64）上，以 root 用户运行下方命令。服务器需要使用 systemd，能访问 GitHub，并已安装 `curl`。

```bash
curl -fsSL https://raw.githubusercontent.com/lwl0820/lpanel/main/deploy/install.sh -o lpanel-install.sh && bash lpanel-install.sh --repository lwl0820/lpanel
```

脚本会下载并校验适合服务器架构的最新正式发行包。在终端执行时，可以只传已确定的选项；省略选项或只写选项名不填值，脚本会按需提示补全。

**使用参数安装（域名 + HTTPS）**：先将自己的域名解析到服务器，开放公网 80 端口和面板端口。阅读 [Let’s Encrypt 服务条款](https://letsencrypt.org/repository/) 后，将示例域名和联系邮箱换成自己的，再运行：

```bash
curl -fsSL https://raw.githubusercontent.com/lwl0820/lpanel/main/deploy/install.sh -o lpanel-install.sh &&
  bash lpanel-install.sh \
    --repository lwl0820/lpanel \
    --username admin \
    --password \
    --port 8443 \
    --login-path /my-entry \
    --domain panel.example.com \
    --auto-cert y \
    --acme-provider letsencrypt \
    --agree-tos \
    --email acme@example.com
```

上面的命令会提示你输入并确认管理员密码，输入时不会显示。需要无人值守安装时，可将 `--password` 改为 `--password="$LPANEL_PASSWORD"`，并提前设置该变量（8 至 72 字节）。通过参数传入的密码可能出现在进程列表中。

- 使用服务器地址访问：改为 `--domain '' --auto-cert n`，去掉 `--acme-provider`、`--agree-tos` 和 `--email`。
- 使用 ZeroSSL 签发证书：阅读 [ZeroSSL 服务条款](https://zerossl.com/legal/terms)，将提供商改为 `--acme-provider zerossl`，并加上 `--acme-eab-file /路径/zerossl-eab`。文件两行依次填写 Key ID 和 HMAC 密钥，仅允许管理员读取。
- 使用本地发行包：添加 `--archive /路径/lpanel_v0.1.2_linux_amd64.tar.gz`（替换为实际路径和服务器架构对应的发行包），并在旁边放置对应的 `.tar.gz.sha256` 校验文件。指定后不会从 GitHub 下载发行包，`--repository` 可省略。
- 查看脚本帮助：`bash lpanel-install.sh --help`。

安装完成后可用 `lpanel manage --help` 查看终端管理命令。 [安装参数和示例](docs/install.md#命令行参数) · [终端管理说明](docs/install.md#在服务器终端管理面板)

## 在线卸载

在安装了 LPanel 的服务器上，以 root 用户运行以下命令。

```bash
curl -fsSL https://raw.githubusercontent.com/lwl0820/lpanel/main/deploy/uninstall.sh -o lpanel-uninstall.sh && bash lpanel-uninstall.sh
```

默认仅卸载面板，Compose 项目继续运行。也可选择同时删除面板登记的项目、容器和项目目录，包括目录内的相对路径挂载数据。卸载后，由面板配置的域名访问入口会停用。删除和保留的内容见[卸载说明](docs/install.md#卸载)。

## 发布版本

在与远端 `main` 同步的待发布提交上创建 `vX.Y.Z` 标签并推送。以 `v0.1.2` 为例：

```bash
git tag -a v0.1.2 -m "LPanel v0.1.2"
git push origin v0.1.2
```

[发布版本工作流](https://github.com/lwl0820/lpanel/actions/workflows/release.yml)会从标签构建 Linux amd64 和 arm64 发行包，校验后将发行包和对应的 SHA-256 校验文件发布到 GitHub Releases。

如果标签已经推送但还没有对应的 Release，可以从 GitHub Actions 手动运行「发布版本」，选择 `main` 并填写已有标签。也可以用 GitHub CLI 运行：

```bash
gh workflow run release.yml --ref main -f tag=v0.1.1
```

手动运行时，工作流仍使用该标签指向的提交来构建发行包。

## 检查与验证

```bash
go test ./cmd/... ./internal/...
go vet ./cmd/... ./internal/...
npm --prefix web run typecheck
npm --prefix web run lint
npm --prefix web run test -- --run
npm --prefix web run build
bash -n deploy/install.sh
bash -n deploy/uninstall.sh
bash -n scripts/build-release.sh
bash tests/uninstall-smoke.sh
```

Go 检查显式限定项目目录，避免扫描前端依赖附带的 Go 示例。沿用开发准备步骤中的工作目录缓存变量。

需要可用 Docker 的实际集成验证：

```bash
LPANEL_DOCKER_INTEGRATION=1 go test ./internal/panel -run TestProjectDockerIntegration -v
bash tests/install-smoke.sh
```

安装脚本测试使用隔离容器中的包管理和系统服务命令替身，不代表真实 apt、systemd 或公网证书签发已通过。当前已有自动化测试、真实 Docker 项目部署和 Nginx 访问联调，详细结果与外部待验收项见[验证记录](docs/validation.md)。
