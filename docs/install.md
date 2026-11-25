# 安装与初始化

## 运行环境

目标系统为 Debian 12/13、Ubuntu 22.04/24.04/26.04 LTS，目标架构为 amd64、arm64，需要 root、systemd 和 apt。系统及架构的实际验证覆盖以交付记录为准。

面板安装在 `/opt/lpanel`，服务为 `lpanel.service`，证书续期由 `lpanel-renew.timer` 调度。后端默认只监听 `127.0.0.1:18080`，前端由 Nginx 提供，默认面板端口为 `8443`。

## 安装入口

将安装脚本下载到服务器后执行：

```bash
sudo bash deploy/install.sh --repository 你的账号/你的仓库
```

发布仓库必须有正式版本标签 `vX.Y.Z`，并提供对应服务器架构的以下附件：

- `lpanel_vX.Y.Z_linux_amd64.tar.gz` 或 `lpanel_vX.Y.Z_linux_arm64.tar.gz`
- 与发行包同名的 `.sha256` 文件，例如 `lpanel_v0.1.0_linux_amd64.tar.gz.sha256`

仓库地址也可以通过 `LPANEL_REPOSITORY` 设置；脚本不会使用虚构的默认仓库。下载最新正式 Release 后会验证校验文件、发行包内容及程序报告的版本。

尚未公开发布时，可将本地构建的发行包和校验文件一起复制到服务器：

```bash
sudo bash deploy/install.sh --archive /服务器路径/lpanel_v0.1.0_linux_amd64.tar.gz
```

## 命令行参数

在终端安装时，可以省略选项或只写选项名不填值。脚本只询问当前安装方式需要且尚未提供的配置。显式传入空字符串（例如 `--domain ''`）不算缺值。执行 `bash deploy/install.sh --help` 可查看用法。

| 参数 | 用途 |
| --- | --- |
| `--repository <账号/仓库>` | 指定 GitHub 发布仓库。在线安装时，依次使用命令行参数、`LPANEL_REPOSITORY` 和交互输入。 |
| `--archive <路径>` | 指定本地发行包。校验文件放在发行包旁。 |
| `--username <账号>` | 设置管理员账号。交互默认值为 `admin`。 |
| `--password <密码>` | 设置单行管理员密码，长度为 8 至 72 字节。密码以 `--` 开头时使用 `--password=<密码>`。省略时隐藏输入并要求确认。 |
| `--port <端口>` | 设置面板端口。交互默认值为 `8443`。 |
| `--login-path <路径>` | 设置专用登录路径，例如 `/abcdef`。默认留空，从 `/` 登录。 |
| `--domain <域名>` | 绑定面板域名。`--domain ''` 使用服务器地址并跳过证书选项。 |
| `--auto-cert y\|n` | 有域名时设置是否申请 HTTPS 证书。交互默认值为 `y`。 |
| `--acme-provider letsencrypt\|zerossl` | 选择证书提供商。直接回车接受 Let’s Encrypt。无人值守时省略此项也使用 Let’s Encrypt。 |
| `--agree-tos` | 同意所选提供商的证书服务条款。交互默认值为 `y`。 [Let’s Encrypt 服务条款](https://letsencrypt.org/repository/) · [ZeroSSL 服务条款](https://zerossl.com/legal/terms) |
| `--email <邮箱>` | 申请证书时的联系邮箱。交互默认使用面板域名最后两段，例如 `abc.xyz.com` 使用 `acme@xyz.com`。`--email ''` 表示留空。 |
| `--acme-eab-file <路径>` | ZeroSSL 凭据文件。第一行填写 Key ID，第二行填写 HMAC 密钥。交互安装可按提示隐藏输入，无人值守时须提供文件。 [ZeroSSL 凭据获取说明](https://zerossl.com/documentation/acme#eab-credentials) |

部署环境已将管理员密码设置为 Shell 变量 `LPANEL_PASSWORD` 时，可通过以下命令直接安装 HTTP 面板。

```bash
sudo bash deploy/install.sh --repository lwl0820/lpanel \
  --username admin --password "$LPANEL_PASSWORD" --port 8443 --domain '' </dev/null
```

需要在安装时用 Let’s Encrypt 申请证书，可改用 `--domain panel.example.com --auto-cert y --agree-tos --email ''`，将示例域名换成自己的域名。

使用 ZeroSSL 无人值守安装时，在证书参数中加上 `--acme-provider zerossl --acme-eab-file /服务器路径/zerossl-eab`。先从 ZeroSSL 获取凭据并写入仅管理员可读取的文件。发行包须支持 ZeroSSL。

使用 `--password` 时，安装运行期间进程列表可能显示密码。若直接键入密码，终端历史也可能记录它。脚本通过标准输入将密码交给面板初始化。手动安装时可省略该参数，使用隐藏输入。

## 在服务器终端管理面板

安装或升级后，可从服务器终端直接运行 `lpanel`。安装程序将 `/usr/local/bin/lpanel` 链接到当前运行版本。修改账号或访问设置需要 `sudo`，无需登录网页。

| 操作 | 命令 |
| --- | --- |
| 查看帮助 | `lpanel manage --help` |
| 修改管理员用户名 | `sudo lpanel manage username <新用户名>` |
| 重置管理员密码 | `sudo lpanel manage password` |
| 查看当前登录地址 | `sudo lpanel manage access` |
| 修改访问域名或 IP | `sudo lpanel manage access --host <域名或IP>` |
| 修改面板端口 | `sudo lpanel manage access --port 9443` |
| 修改登录入口 | `sudo lpanel manage access --login-path /my-entry` |
| 恢复根路径登录 | `sudo lpanel manage access --login-path /` |
| 开启或关闭 HTTPS | `sudo lpanel manage access --https on` 或 `sudo lpanel manage access --https off` |
| 恢复使用服务器地址通过 HTTP 访问 | `sudo lpanel manage access --host auto --https off` |

重置密码时，终端会隐藏两次密码输入。重置成功后，原有网页会话需要重新登录。

访问选项可以一起提交。更换地址时会沿用当前的 HTTP 或 HTTPS 设置。启用 HTTPS 后更换到新地址时，面板会先为新地址准备证书。应用访问设置会短暂重启面板，建议在没有部署或升级任务运行时操作。命令会在确认生效后显示新登录地址。

要为公网 IPv4 或 IPv6 开启 HTTPS，可执行以下命令。请将示例中的 IP 替换成服务器的实际地址。面板会向 Let’s Encrypt 申请证书、安装并自动续期。公网必须可以访问服务器的 80 端口和实际面板端口。[HTTP-01 验证说明](https://letsencrypt.org/docs/challenge-types/#http-01-challenge)

```bash
sudo lpanel manage access --host 203.0.113.10 --https on --agree-tos
```

首次使用 Let’s Encrypt 时，请先阅读服务条款，再通过 `--agree-tos` 确认。签发失败时面板会提示原因并保留原登录入口。[Let’s Encrypt 服务条款](https://letsencrypt.org/repository/)

## 专用登录入口

安装时可指定 `--login-path /abcdef`，也可在提示时填写。路径以 `/` 开头，每一段可用英文字母、数字、短横线和下划线。`/api`、`/assets` 及其子路径供系统使用。留空或填 `/` 时，从根地址登录。

安装完成后会显示完整登录地址，例如 `https://panel.example.com:8443/abcdef`。启用专用入口时，路径区分大小写，末尾无需加 `/`。未登录访问其他地址会收到 404。登录后可以照常打开和刷新面板页面。

登录后，在「面板设置」的访问设置中可以更改或清除登录路径，并复制新的登录地址。如果忘记路径，可通过上面的 `manage access` 命令查看完整登录地址。较早的发行版可在服务器上用 `sudoedit /opt/lpanel/data/config.json` 查看 `login_path`。要在较早版本恢复从根地址登录，将其设为空字符串 `""`，保存后执行以下命令。

```bash
sudo /opt/lpanel/current/lpanel setup-nginx --root /opt/lpanel
sudo systemctl restart lpanel.service
```

## 域名与 HTTPS

安装时填写的绑定域名使用普通域名。请提前将域名解析到服务器，并开放公网 80 端口和实际面板端口。业务网站还使用标准 80/443，静态网站支持通过 HTTP 访问通配符主机名。为公网 IP 开启 HTTPS 请使用终端管理命令。[终端管理说明](#在服务器终端管理面板)

编排项目绑定域名后，应用可通过 `X-Real-IP` 或 `X-Forwarded-For` 读取 Nginx 识别的客户端地址，同一域名也支持 WebSocket。前面有 CDN 或其他代理时，要让应用看到访客地址，需先在 Nginx 中配置可信代理来源。旧版升级后，运行 `sudo /opt/lpanel/current/lpanel setup-nginx --root /opt/lpanel` 可将这些规则应用到已有域名绑定。

未申请证书时，地址为 `http://服务器地址:面板端口`；绑定域名且证书签发成功后，地址为 `https://面板域名:面板端口`。首次签发失败保留 HTTP 并输出服务器 IP 地址入口，脚本显示失败原因，安装结束不声称 HTTPS 已启用；修正网络或解析后可在面板中重试。

安装时用于申请证书的提供商和联系邮箱会保存为默认 ACME 账户。仅安装 HTTP 时，可以登录面板，在「域名与证书」的「ACME 账户管理」添加账户。第一个账户会成为默认账户。

申请证书时可以选择其他账户，自动续期会沿用当前证书的签发账户。账户还有已签发的证书时，面板会阻止删除，即使证书已经过期或域名已解除绑定。删除没有证书的默认账户后，面板会选用最早添加的剩余账户。

## 已有环境与数据

升级时，面板自动把已有 Compose 项目的目录路径写入数据库。已有项目的文件和容器挂载继续使用原来的 ID 目录，新建项目的目录按项目名称分配。

已有可用的 Docker 和 Compose 插件会直接复用，不会卸载替换。已有 Docker 不可用、缺少 Compose 插件或存在冲突软件包时，安装停止并提示用户处理。需要新安装 Docker 时使用 Docker 官方 apt 软件源。

已有非本面板 Nginx、80/443/面板端口或后端端口被占用时，脚本停止，不覆盖旧配置。重复执行已完成安装的脚本会保留管理员和业务数据，并提示使用面板升级入口。

安装过程中下载和解包文件仅放在 `/opt/lpanel/.tmp`。如果首次安装在初始化前中断，脚本不会自动删除已产生的版本目录或卸载系统依赖；先检查错误和已有文件后再处理，避免覆盖数据。

## 卸载

通过官方安装脚本部署的 LPanel，可以在服务器终端运行 [README 的在线卸载命令](../README.md#在线卸载)。脚本需要管理员权限，会先说明影响并等待确认。

Supervisor 托管的程序继续按现有配置运行，程序文件、日志和托管配置保留。如需同时清理某个程序，请在卸载前进入「程序管理」删除该程序，并选择是否删除文件。

- **仅卸载面板（默认）**。移除面板程序、终端命令链接、systemd 服务、续期定时器和面板生成的 Nginx 配置。容器继续运行，项目目录和配置保留。
- **同时清理 Compose 项目**。列出面板登记的项目和将删除的目录。确认后停止并移除这些项目的容器和网络，删除项目记录、对应的域名绑定和整个项目目录。容器内部未持久化的文件，以及 `./data` 这类保存在项目目录里的相对路径挂载数据，都会随项目删除。Docker 数据卷、镜像、项目目录之外的挂载数据和其他 Docker 项目保留。

两种选项都会停用 LPanel 配置的面板和业务域名入口，已签发的证书也会停止自动续期。面板数据库、管理员账号、证书、项目目录之外的静态网站文件，以及 Docker、Nginx 等系统软件保留。已有数据和 Nginx 会触发安装脚本的环境检查，重新安装前需先处理这些保留项。

## 常用维护命令

```bash
sudo systemctl status lpanel.service nginx.service
sudo journalctl -u lpanel.service -n 100
sudo systemctl status lpanel-renew.timer
```

在面板的更新页面可以手动检查和安装新版本，也可以开启“自动更新”。此开关默认关闭，开启后从下一次后台检查起自动安装适用于本机的较新正式版。面板服务启动时检查一次，之后每 24 小时检查一次，关闭浏览器也会执行。发行版切换使用 `current` 指针，持久化配置与数据库位于独立的 `data/` 中，不随程序覆盖。

若从旧版升级后，上传文件或导入编排项目 ZIP 时提示超过 1 MiB，可运行 `sudo /opt/lpanel/current/lpanel setup-nginx --root /opt/lpanel` 重新生成面板的 Nginx 配置，再重试上传。若面板前另有反向代理，也要检查该代理的上传限制。

### 管理本机防火墙

在「服务器管理 → 防火墙」可查看 UFW 状态、启停防火墙，以及添加或删除单个 TCP、UDP 入站端口规则。停用时看到的是下次启用生效的规则。本机允许入站不等于云安全组或外层代理已经放行。

面板不会安装、替换或自动启用防火墙。服务器没有其他防火墙管理程序、且你决定使用 UFW 时，可在 Debian 或 Ubuntu 终端运行 `sudo apt install ufw`，随后回到面板刷新。已经使用其他工具管理规则的服务器，请继续使用原工具。

从旧版升级后，先运行 `sudo /opt/lpanel/current/lpanel setup-nginx --root /opt/lpanel` 更新代理配置。启用时会按当前面板端口检查入站规则；已有来源限制不会自动扩大，遇到无法确认的规则会保留停用状态。面板前另有代理时，以实际连接到本机的代理地址为准。

### 管理交换空间

打开「服务器管理 → 交换空间」可查看整机当前启用的容量、已用量及每个文件或分区的明细。新建时填写单个文件的容量。例如，服务器已有 2 GiB 交换文件，再新建 4 GiB 文件后可以分别管理两项。内核会为每项预留少量空间，实际可用容量会略小于文件大小。

面板创建的文件支持调整容量、停用和重新启用。删除时按路径选择目标，系统原有的交换文件也可单独删除。操作结束后可在列表核对结果。交换分区只读，已启用的分区计入整机汇总。

### 设置时区与时间同步

打开「服务器管理 → 时区与时间同步」可查看服务器当地时间和当前时区。搜索并选择时区后，点击「保存时区」即可生效。例如，北京时间可选择 `Asia/Shanghai`。

「自动校时」控制系统是否持续通过网络校准时间。「同步状态」显示系统当前是否已确认校时。使用 systemd-timesyncd 的服务器也可以点击「立即同步」。即使自动校时已关闭，也能单独校准一次。页面会显示本次结果，校时源不可达时会提示超时。服务器使用其他校时服务时，页面会说明立即同步的可用情况。

### 设置服务器默认 DNS

打开「服务器管理 → DNS 设置」可查看服务器解析域名时使用的默认地址。每行填写一个 IPv4 或 IPv6 地址，然后点击「保存设置」。使用静态解析文件的服务器最多填写三个。已配置专用解析规则的域名会继续使用原来的 DNS。

面板支持管理员维护的静态解析文件，以及使用 systemd-resolved 本地代理的服务器。已有网卡接管默认解析，或解析文件由其他工具生成时，页面会说明修改限制。第一次保存后可点击「恢复原设置」，连续保存多次也能恢复首次修改前的配置。

### 查看资源用量和历史趋势

登录后，首页「服务器资源」显示 CPU 使用率、已用内存和系统盘用量。点击「查看监控详情」可查看处理器型号、核心数、系统负载和各磁盘容量。逻辑 CPU 数包含同一物理核心上的硬件线程，虚拟机显示分配给它的处理器。

系统负载显示最近 1、5、15 分钟内，正在运行、等待 CPU 或处于不可中断等待的平均任务数。面板每分钟记录一次系统负载、CPU 使用率、内存和各磁盘用量，即使浏览器已关闭也会采样。历史趋势可查看最近 30 天的数据，切换负载平均时段或磁盘。停采期间以及升级前没有负载记录的时段，对应曲线留空。

在「服务器管理 → 实时进程」中，点击列标题可按 CPU、内存、磁盘读取或写入速率排序，再按进程号定位。进程占满一个逻辑核心时，CPU 使用率约为 100%。进程明细仅在打开该页时采集，首次采样的速率会在下一次刷新后出现。

## 安装脚本验证

```bash
bash -n deploy/install.sh
bash tests/install-smoke.sh
```

第二条命令使用一次性 Debian 容器，验证交互、混合输入和无人值守安装，以及重复安装保留、Nginx 和端口冲突、证书失败保留 HTTP。测试真实执行 Bash、解包和安装文件写入，包管理、系统服务和证书使用命令替身。目标环境结果见[验收记录](validation.md)。
