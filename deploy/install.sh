#!/usr/bin/env bash
set -euo pipefail
# 安装只管理自己的目录，不接管已有 Nginx 或其他面板。
fail() { echo "安装失败：$*" >&2; exit 1; }
ask() { local text="$1" default="$2"; read -r -p "$text [$default]：" answer; answer="${answer:-$default}"; }
repository="${LPANEL_REPOSITORY:-}"
archive=""
while (($#)); do
  case "$1" in
    --repository) (($# >= 2)) || fail '缺少仓库参数'; repository="$2"; shift 2 ;;
    --archive) (($# >= 2)) || fail '缺少发行包路径'; archive="$2"; shift 2 ;;
    --help) echo '用法：sudo bash install.sh --repository owner/repo，或 --archive /路径/lpanel_vX.Y.Z_linux_ARCH.tar.gz'; exit 0 ;;
    *) fail "未知参数：$1" ;;
  esac
done
[[ "$EUID" == 0 ]] || fail '请使用 root 或 sudo 执行'
root=/opt/lpanel
[[ ! -e "$root/data/config.json" ]] || fail '检测到已有安装，保留账号和数据；请登录面板使用版本升级功能'
[[ -f /etc/os-release ]] || fail '无法识别操作系统'
. /etc/os-release
case "$ID:$VERSION_ID" in
  debian:12|debian:13|ubuntu:22.04|ubuntu:24.04|ubuntu:26.04) ;;
  *) fail '仅支持 Debian 12/13 和 Ubuntu 22.04/24.04/26.04 LTS' ;;
esac
case "$(uname -m)" in x86_64|amd64) architecture=amd64 ;; aarch64|arm64) architecture=arm64 ;; *) fail '仅支持 amd64、arm64' ;; esac
[[ -d /run/systemd/system ]] || fail '需要由 systemd 启动的服务器环境'
command -v apt-get >/dev/null || fail '系统缺少 apt-get'
if command -v nginx >/dev/null || [[ -e /etc/nginx/nginx.conf ]]; then
  fail '检测到已有 Nginx，请使用干净服务器或先自行迁移，安装不会覆盖配置'
fi
# 读取监听端口；精简系统没有 ss 时直接读取 Linux 的监听表。
ask '管理员账号' admin; username="$answer"
[[ -n "$username" ]] || fail '管理员账号不能为空'
read -r -s -p '管理员密码（8 至 72 字节）：' password; echo
read -r -s -p '再次输入密码：' confirmation; echo
[[ "$password" == "$confirmation" ]] || fail '两次密码不一致'
password_size="$(LC_ALL=C printf '%s' "$password" | wc -c)"
((password_size >= 8 && password_size <= 72)) || fail '密码长度须为 8 至 72 字节'
unset confirmation
ask '面板端口' 8443; panel_port="$answer"
[[ "$panel_port" =~ ^[0-9]+$ ]] && ((10#$panel_port >= 1 && 10#$panel_port <= 65535)) || fail '端口无效'
panel_port=$((10#$panel_port))
for candidate in 80 443 "$panel_port" 18080; do
  if command -v ss >/dev/null; then
    [[ -z "$(ss -H -ltn "sport = :$candidate")" ]] || fail "端口 $candidate 已被占用，请先处理冲突"
  else
    hex_port="$(printf '%04X' "$candidate")"
    if awk -v port="$hex_port" '$4=="0A" {split($2, address, ":"); if (toupper(address[2])==port) found=1} END {exit !found}' /proc/net/tcp /proc/net/tcp6; then
      fail "端口 $candidate 已被占用，请先处理冲突"
    fi
  fi
done
[[ "$panel_port" != 18080 ]] || fail '面板端口不能与内部端口 18080 相同'
ask '面板域名（留空则使用服务器地址）' ''; domain="$answer"
auto_cert=n
agree_tos=n
email=''
if [[ -n "$domain" ]]; then
  [[ "$domain" =~ ^[a-zA-Z0-9]([a-zA-Z0-9.-]*[a-zA-Z0-9])?$ && "$domain" == *.* ]] || fail '请输入不带协议和端口的普通域名'
  domain="${domain,,}"
  ask '自动申请 HTTPS 证书？y/n' n; auto_cert="${answer,,}"
  [[ "$auto_cert" == y || "$auto_cert" == n ]] || fail '请输入 y 或 n'
  if [[ "$auto_cert" == y ]]; then
    echo '域名需已解析到本机，并开放公网 80 端口和面板端口。'
    echo '证书服务条款：https://letsencrypt.org/repository/'
    ask '是否同意证书服务条款？y/n' n; agree_tos="${answer,,}"
    [[ "$agree_tos" == y ]] || fail '申请证书需要同意服务条款'
    ask '证书联系邮箱（可留空）' ''; email="$answer"
  fi
fi
if [[ -n "$archive" ]]; then
  [[ -f "$archive" && -f "$archive.sha256" ]] || fail '本地发行包或对应 .sha256 文件不存在'
  archive="$(cd -- "$(dirname -- "$archive")" && pwd)/$(basename -- "$archive")"
else
  if [[ -z "$repository" ]]; then ask 'GitHub 发布仓库（owner/repo）' ''; repository="$answer"; fi
  [[ -n "$repository" ]] || fail '请配置真实发布仓库或指定本地发行包'
fi
if [[ -n "$repository" ]]; then
  [[ "$repository" =~ ^[a-zA-Z0-9_.-]+/[a-zA-Z0-9_.-]+$ ]] || fail '仓库格式须为 owner/repo'
fi
if command -v docker >/dev/null; then
  docker info >/dev/null 2>&1 || fail '已有 Docker 不可用，请先启动或修复 Docker，安装不会替换它'
  docker compose version >/dev/null 2>&1 || fail '已有 Docker 缺少可用的 Compose 插件，请先安装 Compose v2 或更新版本'
  install_docker=n
else
  install_docker=y
  for package in docker.io docker-compose podman-docker containerd runc; do
    if dpkg-query -W -f='${Status}' "$package" 2>/dev/null | grep -q 'install ok installed'; then
      fail "检测到冲突软件包 $package，安装不会擅自卸载，请先自行处理"
    fi
  done
fi
mkdir -p "$root/.tmp" "$root/releases" "$root/data"
chmod 755 "$root" "$root/releases"
chmod 700 "$root/.tmp" "$root/data"
export TMPDIR="$root/.tmp"
work="$(mktemp -d "$TMPDIR/install-XXXXXXXX")"
trap 'unset password; rm -rf -- "$work"' EXIT
export DEBIAN_FRONTEND=noninteractive
apt-get update
apt-get install -y ca-certificates curl gnupg python3 nginx certbot
if [[ "$install_docker" == y ]]; then
  install -m 0755 -d /etc/apt/keyrings
  curl -fsSL "https://download.docker.com/linux/$ID/gpg" -o "$work/docker.asc"
  install -m 0644 "$work/docker.asc" /etc/apt/keyrings/lpanel-docker.asc
  printf 'deb [arch=%s signed-by=/etc/apt/keyrings/lpanel-docker.asc] https://download.docker.com/linux/%s %s stable\n' "$(dpkg --print-architecture)" "$ID" "$VERSION_CODENAME" > /etc/apt/sources.list.d/lpanel-docker.list
  apt-get update
  apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
  systemctl enable --now docker.service
fi
docker info >/dev/null 2>&1 || fail 'Docker 服务不可用'
docker compose version >/dev/null 2>&1 || fail 'Compose 插件不可用'
if [[ -z "$archive" ]]; then
  curl -fsSL "https://api.github.com/repos/$repository/releases/latest" -o "$work/release.json"
  version="$(python3 - "$work/release.json" <<'PY'
import json, sys
print(json.load(open(sys.argv[1]))['tag_name'])
PY
)"
  [[ "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || fail '最新发行标签不是 vX.Y.Z'
  name="lpanel_${version}_linux_${architecture}.tar.gz"
  python3 - "$work/release.json" "$name" > "$work/urls" <<'PY'
import json, sys
assets={item['name']: item['browser_download_url'] for item in json.load(open(sys.argv[1]))['assets']}
for name in (sys.argv[2], sys.argv[2]+'.sha256'):
    if name not in assets: raise SystemExit('发行附件缺失：'+name)
    print(assets[name])
PY
  mapfile -t urls < "$work/urls"
  curl -fsSL "${urls[0]}" -o "$work/$name"
  curl -fsSL "${urls[1]}" -o "$work/$name.sha256"
  archive="$work/$name"
else
  name="$(basename -- "$archive")"
  [[ "$name" =~ ^lpanel_(v[0-9]+\.[0-9]+\.[0-9]+)_linux_${architecture}\.tar\.gz$ ]] || fail '发行包名称或架构不匹配'
  version="${BASH_REMATCH[1]}"
fi
expected="$(awk 'NR==1 {print $1}' "$archive.sha256")"
[[ "$expected" =~ ^[a-fA-F0-9]{64}$ ]] || fail '校验文件格式错误'
actual="$(sha256sum "$archive")"; actual="${actual%% *}"
[[ "${expected,,}" == "$actual" ]] || fail '发行包校验失败'
mkdir "$work/release"
# 安装和升级接收相同的平铺发行格式；检查后才解包到候选目录。
python3 - "$archive" "$work/release" <<'PY'
import pathlib, sys, tarfile
with tarfile.open(sys.argv[1], 'r:gz') as bundle:
    for member in bundle.getmembers():
        path=pathlib.PurePosixPath(member.name)
        if path.is_absolute() or '..' in path.parts or not path.parts or path.parts[0] not in ('lpanel','web','deploy') or not (member.isfile() or member.isdir()):
            raise SystemExit('发行包包含不支持的文件或路径')
    bundle.extractall(sys.argv[2])
PY
for file in lpanel web/index.html deploy/lpanel.service deploy/lpanel-renew.service deploy/lpanel-renew.timer; do
  [[ -f "$work/release/$file" ]] || fail "发行包缺少 $file"
done
chmod 755 "$work/release/lpanel"
[[ "$("$work/release/lpanel" version)" == "$version" ]] || fail '程序版本与发行标签不一致'
[[ ! -e "$root/releases/$version" && ! -e "$root/current" ]] || fail '已有版本目录，请检查上次安装结果后重试'
mv "$work/release" "$root/releases/$version"
ln -s "$root/releases/$version" "$root/current"
printf '%s\n' "$password" | "$root/current/lpanel" init --root "$root" --username "$username" --port "$panel_port" --listen 127.0.0.1:18080 --repository "$repository"
unset password
install -m 0644 "$root/current/deploy/lpanel.service" /etc/systemd/system/lpanel.service
install -m 0644 "$root/current/deploy/lpanel-renew.service" /etc/systemd/system/lpanel-renew.service
install -m 0644 "$root/current/deploy/lpanel-renew.timer" /etc/systemd/system/lpanel-renew.timer
# 只移除本次新装 Nginx 的默认站点链接，不触碰既有用户配置。
rm -f /etc/nginx/sites-enabled/default
systemctl daemon-reload
systemctl enable --now nginx.service lpanel.service
"$root/current/lpanel" setup-nginx --root "$root"
systemctl enable --now lpanel-renew.timer
server_address="$(hostname -I | awk '{print $1}')"; server_address="${server_address:-服务器地址}"
[[ "$server_address" != *:* ]] || server_address="[$server_address]"
address="$server_address"
[[ -z "$domain" ]] || address="$domain"
url="http://$address:$panel_port"
certificate_failed=n
if [[ -n "$domain" ]]; then
  bind_args=(bind-panel --root "$root" --domain "$domain")
  if [[ "$auto_cert" == y ]]; then bind_args+=(--auto-cert --agree-tos --email "$email"); fi
  if "$root/current/lpanel" "${bind_args[@]}"; then
    [[ "$auto_cert" != y ]] || url="https://$domain:$panel_port"
  else
    certificate_failed=y
    url="http://$server_address:$panel_port"
    echo '域名或证书配置失败，请根据上方原因处理后在面板中重试；当前保留 HTTP 入口。' >&2
  fi
  systemctl restart lpanel.service
fi
for attempt in {1..15}; do
  if curl -fsS http://127.0.0.1:18080/api/v1/health > "$work/health.json"; then break; fi
  sleep 1
done
python3 - "$work/health.json" "$version" <<'PY'
import json, sys
state=json.load(open(sys.argv[1]))
if state.get('status')!='ok' or state.get('version')!=sys.argv[2]: raise SystemExit('面板服务健康检查失败')
PY
systemctl is-active --quiet nginx.service lpanel.service || fail '面板或 Nginx 服务未正常运行'
if [[ -n "$domain" ]]; then
  curl -fsS --resolve "$domain:$panel_port:127.0.0.1" "$url" > /dev/null || fail 'Nginx 面板入口验证失败，请检查服务日志'
else
  curl -fsS "http://127.0.0.1:$panel_port" > /dev/null || fail 'Nginx 面板入口验证失败，请检查服务日志'
fi
echo "面板安装完成，访问地址：$url"
[[ "$certificate_failed" != y ]] || echo '证书未启用，HTTP 安装已完成，HTTPS 仍需重试。'
