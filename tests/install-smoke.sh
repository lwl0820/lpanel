#!/usr/bin/env bash
set -euo pipefail
# 在一次性 Debian 容器里运行安装脚本，命令替身只验证安装分支与参数契约。
project_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$project_root"
mkdir -p .tmp/install-tests/bin .tmp/install-tests/package/deploy .tmp/install-tests/package/web
fixture="$project_root/.tmp/install-tests"
cp deploy/lpanel*.service deploy/lpanel-renew.timer "$fixture/package/deploy/"
printf '<html>安装测试</html>\n' > "$fixture/package/web/index.html"
cat > "$fixture/package/lpanel" <<'SH'
#!/bin/bash
set -euo pipefail
[[ "$1" != version ]] || { echo v0.1.0; exit; }
printf '%s\n' "$*" >> /opt/lpanel/.tmp/cli-arguments
case "$1" in
  init)
    read -r password
    [[ "$password" == test-password ]] || exit 9
    mkdir -p /opt/lpanel/data
    printf '{"initialized":true}\n' > /opt/lpanel/data/config.json ;;
  setup-nginx) ;;
  bind-panel)
    if [[ "${FAIL_CERT:-0}" == 1 ]]; then echo '测试签发失败：域名无法验证' >&2; exit 1; fi
    echo '面板访问地址：https://panel.example.com:8443' ;;
  *) exit 9 ;;
esac
SH
chmod 755 "$fixture/package/lpanel"
for command in docker apt-get systemctl; do
  printf '#!/bin/sh\nexit 0\n' > "$fixture/bin/$command"
  chmod 755 "$fixture/bin/$command"
done
cat > "$fixture/bin/curl" <<'SH'
#!/bin/sh
printf '{"status":"ok","version":"v0.1.0"}\n'
SH
cat > "$fixture/bin/hostname" <<'SH'
#!/bin/sh
echo 192.0.2.10
SH
chmod 755 "$fixture/bin/hostname"
cat > "$fixture/bin/ss" <<'SH'
#!/bin/sh
[ "${PORT_CONFLICT:-0}" != 1 ] || echo 'LISTEN 0 128 0.0.0.0:8443'
exit 0
SH
chmod 755 "$fixture/bin/curl" "$fixture/bin/ss"
case "$(uname -m)" in x86_64) arch=amd64 ;; aarch64) arch=arm64 ;; *) echo '测试架构不支持' >&2; exit 1 ;; esac
archive="lpanel_v0.1.0_linux_${arch}.tar.gz"
tar -czf "$fixture/$archive" -C "$fixture/package" lpanel web deploy
(cd "$fixture" && sha256sum "$archive" > "$archive.sha256")
docker run --rm --interactive --mount "type=bind,src=$project_root,dst=/source,readonly" --mount "type=bind,src=$fixture,dst=/fixture,readonly" -e "ARCHIVE=$archive" python:3.11-slim-bookworm bash -s <<'SH'
set -euo pipefail
mkdir -p /run/systemd/system /work/.tmp
cd /work
export TMPDIR=/work/.tmp
export PATH="/fixture/bin:$PATH"
archive="/fixture/$ARCHIVE"
# 正常首次安装使用真实 bash、tar、Python 和文件操作，仅系统服务/包管理为替身。
printf 'admin\ntest-password\ntest-password\n8443\n\n' | bash /source/deploy/install.sh --archive "$archive" > /work/.tmp/install-result
[[ -f /opt/lpanel/data/config.json && -L /opt/lpanel/current ]]
grep -q '面板安装完成，访问地址：http://' /work/.tmp/install-result
! grep -q 'test-password' /opt/lpanel/.tmp/cli-arguments
[[ -f /etc/systemd/system/lpanel.service && -f /etc/systemd/system/lpanel-renew.timer ]]
cp /opt/lpanel/data/config.json /work/.tmp/before-config
if bash /source/deploy/install.sh --archive "$archive" > /work/.tmp/repeat-result 2>&1; then exit 11; fi
cmp /work/.tmp/before-config /opt/lpanel/data/config.json
grep -q '已有安装' /work/.tmp/repeat-result
rm -rf /opt/lpanel
# 外部 Nginx 必须在写安装配置前被拒绝。
mkdir -p /etc/nginx
touch /etc/nginx/nginx.conf
if bash /source/deploy/install.sh --archive "$archive" > /work/.tmp/nginx-result 2>&1; then exit 12; fi
grep -q '已有 Nginx' /work/.tmp/nginx-result
[[ ! -e /opt/lpanel/data/config.json ]]
rm /etc/nginx/nginx.conf
# 端口冲突同样不能继续初始化。
if printf 'admin\ntest-password\ntest-password\n8443\n' | PORT_CONFLICT=1 bash /source/deploy/install.sh --archive "$archive" > /work/.tmp/port-result 2>&1; then exit 13; fi
grep -q '已被占用' /work/.tmp/port-result
[[ ! -e /opt/lpanel/data/config.json ]]
# 签发失败保留 HTTP，不能把 HTTPS 地址报为成功。
printf 'admin\ntest-password\ntest-password\n8443\npanel.example.com\ny\ny\n\n' | FAIL_CERT=1 bash /source/deploy/install.sh --archive "$archive" > /work/.tmp/cert-result 2>&1
grep -q '证书未启用' /work/.tmp/cert-result
grep -q '访问地址：http://192.0.2.10:8443' /work/.tmp/cert-result
! grep -q '访问地址：https://' /work/.tmp/cert-result
[[ -f /opt/lpanel/data/config.json ]]
echo '安装脚本分支验证通过：首次安装、参数保密、重跑保留、Nginx/端口冲突、证书失败保留 HTTP。'
SH
