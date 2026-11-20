# LPanel

使用 Go、Vue 3 和 Element Plus 编写的中文服务器管理面板。Nginx 提供前端和网站访问入口，面板通过 Docker Compose 部署应用，通过 Let’s Encrypt 管理 HTTPS 证书。

## 首版功能

- 单管理员登录，安装时设置账号、密码和访问端口。
- Compose 配置与环境变量输入、端口扫描、部署、启停、重启、状态及日志。
- 为已发布的 HTTP/HTTPS 应用端口绑定域名，支持 WebSocket。
- 指定服务器目录添加静态网站，支持单页应用路由回退。
- 域名旁勾选自动申请证书，签发成功后启用 HTTPS，并自动续期。
- 检查 GitHub Releases 正式版本，手动发起升级，启动失败时恢复上一版程序。
- 一键安装，支持安装时绑定域名并签发面板证书。

## 运行方式

面板以 Go 二进制运行，由 systemd 管理。Nginx、Docker 和 Compose v2 运行在宿主机，数据使用本地 SQLite 保存。安装根目录为 `/opt/lpanel`，程序和前端按版本放在 `releases/` 中，`current` 指向运行版本，持久化数据放在 `data/`。

目标系统为 Debian 12/13、Ubuntu 22.04/24.04/26.04 LTS，目标架构为 amd64 和 arm64。实际验证结果以交付记录为准。

面板默认访问端口为 `8443`。未申请证书时使用 `http://服务器地址:端口`；面板域名签发证书成功后使用 `https://面板域名:端口`。业务网站使用标准的 80/443 端口。

## 域名与证书前提

需要用户将域名解析到服务器，并在防火墙及云安全组开放公网 80 和实际访问端口。证书采用 HTTP-01 验证；即使面板使用自定义端口，验证仍通过 80 端口完成。首版支持普通域名，不接入 DNS 服务商，不支持泛域名证书。

首次签发失败时保留 HTTP 入口，并显示原因；修正解析或端口条件后可重试。已有证书续期失败时，保留当前 HTTPS 配置。

## 数据保留

删除 Compose 项目时移除容器、网络和面板绑定，保留数据卷和挂载目录；删除静态网站不删除服务器上的内容目录。升级切换程序与前端版本，保留管理员、项目、网站和证书数据。

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

在 Debian 12/13 或 Ubuntu 22.04/24.04/26.04 LTS 服务器（amd64 或 arm64）的终端运行下方命令。服务器需要使用 systemd，能访问 GitHub，并已安装 `curl` 和 `sudo`。

```bash
curl -fsSL https://raw.githubusercontent.com/lwl0820/lpanel/main/deploy/install.sh -o lpanel-install.sh && sudo bash lpanel-install.sh --repository lwl0820/lpanel
```

脚本会下载并校验适合服务器架构的最新正式发行包。按提示设置管理员账号、密码和面板端口，也可以绑定域名并申请 HTTPS 证书。

详细步骤、端口要求和维护命令见[安装说明](docs/install.md)。

## 在线卸载

在安装了 LPanel 的服务器上运行：

```bash
curl -fsSL https://raw.githubusercontent.com/lwl0820/lpanel/main/deploy/uninstall.sh -o lpanel-uninstall.sh && sudo bash lpanel-uninstall.sh
```

默认仅卸载面板，Compose 项目继续运行。也可选择同时删除面板登记的项目、容器和项目目录，包括目录内的相对路径挂载数据。卸载后，由面板配置的域名访问入口会停用。删除和保留的内容见[卸载说明](docs/install.md#卸载)。

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
