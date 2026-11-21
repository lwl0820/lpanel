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
if [[ "$1" == bind-panel && "${2:-}" == --help ]]; then
  if [[ "${LEGACY_RELEASE:-0}" != 1 ]]; then printf '%s\n' '  -acme-eab-file string'; fi
  exit 0
fi
if [[ "$1" == init && "${2:-}" == --help ]]; then
  if [[ "${LEGACY_RELEASE:-0}" != 1 ]]; then printf '%s\n' '  -login-path string'; fi
  exit 0
fi
printf '%s\n' "$*" >> /opt/lpanel/.tmp/cli-arguments
case "$1" in
  init)
    if [[ "${LEGACY_RELEASE:-0}" == 1 && " $* " == *' --login-path '* ]]; then exit 9; fi
    IFS= read -r password
    [[ "$password" == "${EXPECTED_PASSWORD:-test-password}" ]] || exit 9
    mkdir -p /opt/lpanel/data
    printf '{"initialized":true}\n' > /opt/lpanel/data/config.json ;;
  setup-nginx) ;;
  bind-panel)
    if [[ " $* " == *' --acme-provider zerossl '* ]]; then
      [[ "${@: -2:1}" == --acme-eab-file ]]
      credentials="${@: -1}"
      [[ "$(stat -c '%a' "$credentials")" == 600 ]]
      mapfile -t lines < "$credentials"
      [[ "${lines[0]}" == test-zerossl-key-id && "${lines[1]}" == test-zerossl-hmac-key ]]
    fi
    if [[ "${FAIL_CERT:-0}" == 1 ]]; then echo '测试签发失败：域名无法验证' >&2; exit 1; fi
    echo '面板访问地址：https://panel.example.com:8443' ;;
  *) exit 9 ;;
esac
SH
chmod 755 "$fixture/package/lpanel"
for command in docker systemctl; do
  printf '#!/bin/sh\nexit 0\n' > "$fixture/bin/$command"
  chmod 755 "$fixture/bin/$command"
done
cat > "$fixture/bin/apt-get" <<'SH'
#!/bin/sh
printf '%s\n' "$*" >> /opt/lpanel/.tmp/apt-arguments
SH
cat > "$fixture/bin/curl" <<'SH'
#!/bin/sh
printf '%s\n' "$*" >> /opt/lpanel/.tmp/curl-arguments
if [ "${RETRY_HEALTH:-0}" = 1 ] && [ ! -e /opt/lpanel/.tmp/health-retried ]; then
  touch /opt/lpanel/.tmp/health-retried
  case " $* " in *' -fsS '*) echo 'curl: (7) 测试连接失败' >&2 ;; esac
  exit 7
fi
case " $* " in
  *' -w %{http_code} '*) printf '200' ;;
  *) printf '{"status":"ok","version":"v0.1.0"}\n' ;;
esac
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
chmod 755 "$fixture/bin/apt-get" "$fixture/bin/curl" "$fixture/bin/ss"
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
printf '\ntest-password\ntest-password\n\n\n' | bash /source/deploy/install.sh --archive "$archive" > /work/.tmp/install-result
[[ -f /opt/lpanel/data/config.json && -L /opt/lpanel/current ]]
[[ "$(readlink /usr/local/bin/lpanel)" == /opt/lpanel/current/lpanel ]]
[[ "$(lpanel version)" == v0.1.0 ]]
grep -q '面板安装完成，访问地址：http://' /work/.tmp/install-result
grep -q -- '--username admin --port 8443' /opt/lpanel/.tmp/cli-arguments
if grep -q 'test-password' /opt/lpanel/.tmp/cli-arguments; then exit 10; fi
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
# 使用伪终端补齐省略和未填值的选项，签发失败时保留自定义登录路径。
FAIL_CERT=1 python3 - "$archive" > /work/.tmp/cert-result 2>&1 <<'PY'
import os
import pty
import subprocess
import sys

master, terminal = pty.openpty()
try:
    process = subprocess.Popen([
        "bash", "/source/deploy/install.sh", "--archive",
        "--password", "--port", "8443", "--login-path",
    ], stdin=terminal)
    answers = f"mixed-admin\ntest-password\ntest-password\nPANEL.abc.example.com\n\n\n\n\n/recovery/entry\n{sys.argv[1]}\n"
    os.write(master, answers.encode())
    sys.exit(process.wait(timeout=60))
finally:
    os.close(terminal)
    os.close(master)
PY
grep -q '证书提供商.*\[letsencrypt\]' /work/.tmp/cert-result
grep -q '面板登录路径' /work/.tmp/cert-result
grep -q '本地发行包路径' /work/.tmp/cert-result
grep -q '证书未启用' /work/.tmp/cert-result
grep -Fxq '面板安装完成，访问地址：http://192.0.2.10:8443/recovery/entry' /work/.tmp/cert-result
grep -q 'http://127\.0\.0\.1:18080/api/v1/health$' /opt/lpanel/.tmp/curl-arguments
grep -q 'http://127\.0\.0\.1:8443/recovery/entry$' /opt/lpanel/.tmp/curl-arguments
if grep -q '访问地址：https://' /work/.tmp/cert-result; then exit 18; fi
[[ -f /opt/lpanel/data/config.json ]]
grep -q -- '--username mixed-admin --port 8443' /opt/lpanel/.tmp/cli-arguments
grep -q -- '--login-path /recovery/entry$' /opt/lpanel/.tmp/cli-arguments
grep -Fxq -- 'bind-panel --root /opt/lpanel --domain panel.abc.example.com --auto-cert --agree-tos --email acme@example.com' /opt/lpanel/.tmp/cli-arguments
rm -rf /opt/lpanel
# 自定义端口用于健康检查，首次探测失败后安静重试。关闭标准输入并保留密码原文。
cli_password=' leading $pass\!?# '
LEGACY_RELEASE=1 RETRY_HEALTH=1 EXPECTED_PASSWORD="$cli_password" bash /source/deploy/install.sh \
  --archive "$archive" --username cli-admin --password "$cli_password" --port 09443 --domain '' \
  </dev/null > /work/.tmp/cli-http-result 2>&1
grep -q '访问地址：http://192.0.2.10:9443' /work/.tmp/cli-http-result
grep -q -- '--username cli-admin --port 9443' /opt/lpanel/.tmp/cli-arguments
[[ "$(grep -c 'http://127\.0\.0\.1:9443/api/v1/health$' /opt/lpanel/.tmp/curl-arguments)" == 2 ]]
if grep -Fq 'curl: (7)' /work/.tmp/cli-http-result; then exit 19; fi
if grep -Fq -- "$cli_password" /work/.tmp/cli-http-result /opt/lpanel/.tmp/cli-arguments; then exit 14; fi
rm -rf /opt/lpanel
# 未提供证书邮箱时才读取输入。显式空邮箱可直接走 HTTPS 分支。
if bash /source/deploy/install.sh --archive "$archive" --username cli-admin --password test-password \
  --port 8443 --domain panel.example.com --auto-cert y --agree-tos \
  </dev/null > /work/.tmp/eof-result 2>&1; then exit 15; fi
grep -q '缺少证书联系邮箱' /work/.tmp/eof-result
[[ ! -e /opt/lpanel ]]
bash /source/deploy/install.sh --archive "$archive" --username cli-admin --password test-password \
  --port 8443 --domain panel.example.com --auto-cert y --agree-tos --email '' --login-path '' \
  </dev/null > /work/.tmp/cli-https-result 2>&1
grep -q '访问地址：https://panel.example.com:8443' /work/.tmp/cli-https-result
grep -q -- '--resolve panel\.example\.com:8443:127\.0\.0\.1 https://panel\.example\.com:8443/api/v1/health$' /opt/lpanel/.tmp/curl-arguments
grep -Fxq -- 'bind-panel --root /opt/lpanel --domain panel.example.com --auto-cert --agree-tos --email ' /opt/lpanel/.tmp/cli-arguments
rm -rf /opt/lpanel
# 定制路径要求新版发行包。拒绝旧包时尚未安装 Nginx，随后可以用新版重试。
entry_args=(--archive "$archive" --username cli-admin --password test-password --port 8443
  --domain panel.example.com --auto-cert y --agree-tos --email '')
if bash /source/deploy/install.sh "${entry_args[@]}" --login-path /api/login </dev/null > /work/.tmp/invalid-login-path 2>&1; then exit 22; fi
grep -q '该登录路径已被系统使用' /work/.tmp/invalid-login-path
[[ ! -e /opt/lpanel ]]
if LEGACY_RELEASE=1 bash /source/deploy/install.sh "${entry_args[@]}" --login-path /private-panel </dev/null > /work/.tmp/legacy-login-path 2>&1; then exit 23; fi
grep -q '当前发行包暂不支持自定义登录路径' /work/.tmp/legacy-login-path
[[ ! -e /opt/lpanel/current && ! -e /opt/lpanel/data/config.json ]]
if grep -q 'nginx' /opt/lpanel/.tmp/apt-arguments; then exit 24; fi
bash /source/deploy/install.sh "${entry_args[@]}" --login-path /private-panel </dev/null > /work/.tmp/custom-https 2>&1
grep -Fxq '面板安装完成，访问地址：https://panel.example.com:8443/private-panel' /work/.tmp/custom-https
grep -q 'http://127\.0\.0\.1:18080/api/v1/health$' /opt/lpanel/.tmp/curl-arguments
grep -Fq -- '--resolve panel.example.com:8443:127.0.0.1 -o /dev/null -w %{http_code} https://panel.example.com:8443/private-panel' /opt/lpanel/.tmp/curl-arguments
rm -rf /opt/lpanel
# ZeroSSL 使用私有文件传递凭据，公开发行仍可用旧二进制完成 Let’s Encrypt 安装。
printf '%s\r\n' test-zerossl-key-id test-zerossl-hmac-key > /work/.tmp/zerossl-eab
zerossl_args=(--archive "$archive" --username cli-admin --password test-password --port 8443
  --domain panel.example.com --auto-cert y --agree-tos --email admin@example.com
  --acme-provider zerossl --acme-eab-file /work/.tmp/zerossl-eab)
if LEGACY_RELEASE=1 bash /source/deploy/install.sh "${zerossl_args[@]}" </dev/null > /work/.tmp/legacy-result 2>&1; then exit 20; fi
grep -q '当前发行包暂不支持 ZeroSSL' /work/.tmp/legacy-result
[[ ! -e /opt/lpanel/current && ! -e /opt/lpanel/data/config.json ]]
bash /source/deploy/install.sh "${zerossl_args[@]}" </dev/null > /work/.tmp/zerossl-result 2>&1
grep -q 'ZeroSSL 服务条款' /work/.tmp/zerossl-result
grep -q '访问地址：https://panel.example.com:8443' /work/.tmp/zerossl-result
grep -q -- '--email admin@example.com --acme-provider zerossl --acme-eab-file ' /opt/lpanel/.tmp/cli-arguments
if grep -Fq 'test-zerossl-' /work/.tmp/zerossl-result /opt/lpanel/.tmp/cli-arguments; then exit 21; fi
rm -rf /opt/lpanel
if bash /source/deploy/install.sh --password --domain '' </dev/null > /work/.tmp/missing-value-result 2>&1; then exit 16; fi
grep -q -- '--password 缺少参数值' /work/.tmp/missing-value-result
if bash /source/deploy/install.sh --username cli-admin --password '' </dev/null > /work/.tmp/empty-password-result 2>&1; then exit 17; fi
grep -q '密码长度须为 8 至 72 字节' /work/.tmp/empty-password-result
[[ ! -e /opt/lpanel ]]
rm -- /usr/local/bin/lpanel
printf '其他程序\n' > /usr/local/bin/lpanel
bash /source/deploy/install.sh --archive "$archive" --username admin --password test-password --port 8443 --domain '' \
  </dev/null > /work/.tmp/command-conflict-result 2>&1
grep -Fxq '其他程序' /usr/local/bin/lpanel
grep -q '无法创建命令链接' /work/.tmp/command-conflict-result
echo '安装脚本分支验证通过，覆盖交互、无人值守、登录路径、凭据传递、发行包兼容、重跑保护、端口冲突和证书失败回退。'
SH
