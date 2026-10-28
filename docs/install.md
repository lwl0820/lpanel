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

脚本会依次询问管理员账号、隐藏输入的密码及确认、面板端口、可选域名和是否自动申请证书。申请证书时还需同意证书服务条款，联系邮箱可留空。密码只通过标准输入传给初始化命令，不作为命令行参数传递。

## 域名与 HTTPS

提前将域名解析到服务器，并开放公网 80 端口和实际面板端口。业务网站还使用标准 80/443。HTTP-01 验证始终使用 80，不因面板使用自定义端口而改变。首版不支持泛域名和 DNS 服务商验证。

未申请证书时，地址为 `http://服务器地址:面板端口`；绑定域名且证书签发成功后，地址为 `https://面板域名:面板端口`。首次签发失败保留 HTTP 并输出服务器 IP 地址入口，脚本显示失败原因，安装结束不声称 HTTPS 已启用；修正网络或解析后可在面板中重试。

## 已有环境与数据

已有可用的 Docker 和 Compose 插件会直接复用，不会卸载替换。已有 Docker 不可用、缺少 Compose 插件或存在冲突软件包时，安装停止并提示用户处理。需要新安装 Docker 时使用 Docker 官方 apt 软件源。

已有非本面板 Nginx、80/443/面板端口或后端端口被占用时，脚本停止，不覆盖旧配置。重复执行已完成安装的脚本会保留管理员和业务数据，并提示使用面板升级入口。

安装过程中下载和解包文件仅放在 `/opt/lpanel/.tmp`。如果首次安装在初始化前中断，脚本不会自动删除已产生的版本目录或卸载系统依赖；先检查错误和已有文件后再处理，避免覆盖数据。

## 常用维护命令

```bash
sudo systemctl status lpanel.service nginx.service
sudo journalctl -u lpanel.service -n 100
sudo systemctl status lpanel-renew.timer
```

版本更新通过面板的设置页检查并发起。发行版切换使用 `current` 指针，持久化配置与数据库位于独立的 `data/` 中，不随程序覆盖。

## 安装脚本验证

```bash
bash -n deploy/install.sh
bash tests/install-smoke.sh
```

第二条命令使用一次性 Debian 容器，真实执行 bash、解包、初始化文件和服务文件安装；包管理、系统服务及证书使用命令替身，覆盖首次安装、密码不进入参数、重复安装保留、Nginx/端口冲突、证书失败保留 HTTP。它不代表真实 apt 安装、systemd 启动、服务器重启或公网签发已经验收。
