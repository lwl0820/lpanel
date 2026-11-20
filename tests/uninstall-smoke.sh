#!/usr/bin/env bash
set -euo pipefail

project_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$project_root"
mkdir -p .tmp
fixture="$(mktemp -d -- "$project_root/.tmp/uninstall-tests.XXXXXXXX")"
trap 'rm -rf -- "$fixture"' EXIT
export TMPDIR="$fixture"
export UNINSTALL_TEST_ROOT="$fixture"
mkdir -p "$fixture/bin"
export PATH="$fixture/bin:$PATH"
project_id=0123456789abcdef01234567
export UNINSTALL_TEST_PROJECT="$project_id"
root="$fixture/panel"
command_link="$fixture/bin/lpanel"
config="$fixture/nginx/lpanel.conf"
project="$root/data/projects/$project_id"

# 测试副本重定向系统路径，命令替身保证宿主机服务和 Docker 资源不参与。
python3 - "$fixture" <<'PY'
from pathlib import Path
import shlex
import sys

fixture = Path(sys.argv[1])
script = Path("deploy/uninstall.sh").read_text()
for original, replacement in {
    '[[ "$EUID" == 0 ]]': '[[ -n "$UNINSTALL_TEST_ROOT" ]]',
    "root=/opt/lpanel": "root=" + shlex.quote(str(fixture / "panel")),
    "command_link=/usr/local/bin/lpanel": "command_link=" + shlex.quote(str(fixture / "bin/lpanel")),
    "unit_dir=/etc/systemd/system": "unit_dir=" + shlex.quote(str(fixture / "units")),
    "nginx_conf=/etc/nginx/conf.d/lpanel.conf": "nginx_conf=" + shlex.quote(str(fixture / "nginx/lpanel.conf")),
}.items():
    assert script.count(original) == 1
    script = script.replace(original, replacement)
(fixture / "uninstall.sh").write_text(script)
PY

cat > "$fixture/bin/shim" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
echo "${0##*/} $*" >> "$UNINSTALL_TEST_ROOT/calls"
case "${0##*/} $1" in
  'docker compose')
    project_dir="$UNINSTALL_TEST_ROOT/panel/data/projects/$UNINSTALL_TEST_PROJECT"
    expected=(compose --project-name "lpanel-$UNINSTALL_TEST_PROJECT" --project-directory "$project_dir"
      --env-file "$project_dir/.env" -f "$project_dir/compose.yaml" down --remove-orphans)
    [[ $# -eq ${#expected[@]} && "$*" == "${expected[*]}" ]]
    [[ -f "$project_dir/data/content" ]]
    [[ "${UNINSTALL_TEST_FAILURE:-}" != compose ]] ;;
  'nginx -t')
    [[ ! -e "$UNINSTALL_TEST_ROOT/nginx/lpanel.conf" ]]
    [[ "${UNINSTALL_TEST_FAILURE:-}" != config ]] ;;
  'systemctl reload') [[ "${UNINSTALL_TEST_FAILURE:-}" != reload ]] ;;
  'systemctl is-active'|'systemctl disable'|'systemctl stop'|'systemctl daemon-reload') ;;
  *) exit 2 ;;
esac
SH
chmod +x "$fixture/bin/shim"
for command in docker nginx systemctl; do
  ln -s shim "$fixture/bin/$command"
done

setup() {
  rm -rf -- "$root" "$fixture/units" "$fixture/nginx" "$fixture/outside"
  rm -f -- "$command_link"
  : > "$fixture/calls"
  mkdir -p "$root/releases/v0.1.0" "$root/data/acme" "$project/data" \
    "$root/data/projects/unregistered" "$fixture/units" "$fixture/nginx" "$fixture/outside"
  ln -s releases/v0.1.0 "$root/current"
  ln -s "$root/current/lpanel" "$command_link"
  ln -s "$fixture/outside" "$project/outside-link"
  touch "$root/releases/v0.1.0/lpanel" "$root/data/acme/private-key" "$project/data/content" \
    "$project/compose.yaml" "$project/.env" "$fixture/outside/content" "$fixture/nginx/other.conf"
  printf '# 本文件由 LPanel 管理\n' > "$config"
  printf 'ExecStart=%s/current/lpanel serve --root %s\n' "$root" "$root" > "$fixture/units/lpanel.service"
  printf 'ExecStart=%s/current/lpanel renew --root %s\n' "$root" "$root" > "$fixture/units/lpanel-renew.service"
  printf 'Description=LPanel 自动检查证书续期\n' > "$fixture/units/lpanel-renew.timer"
  python3 - "$root/data/panel.db" "$project_id" <<'PY'
import json
import sqlite3
import sys

with sqlite3.connect(sys.argv[1]) as db:
    db.execute("CREATE TABLE projects (id TEXT, name TEXT)")
    db.execute("CREATE TABLE bindings (payload TEXT)")
    db.execute("INSERT INTO projects VALUES (?, '示例项目')", (sys.argv[2],))
    db.executemany("INSERT INTO bindings VALUES (?)", [
        (json.dumps({"kind": kind, "resource_id": sys.argv[2]}),) for kind in ("project", "site")
    ])
PY
}

uninstall() {
  printf '%s' "$1" | UNINSTALL_TEST_FAILURE="${2:-}" bash "$fixture/uninstall.sh" > "$fixture/output" 2>&1
}

setup
uninstall $'y\nn\n'
[[ ! -s "$fixture/calls" && -f "$project/data/content" && -f "$config" ]]
[[ -L "$command_link" ]]

uninstall $'\ny\n'
[[ -f "$project/data/content" && -f "$root/data/panel.db" && -f "$fixture/nginx/other.conf" ]]
[[ ! -e "$root/current" && ! -L "$root/current" && ! -e "$root/releases" ]]
[[ ! -L "$command_link" ]]
[[ ! -e "$config" && ! -e "$fixture/units/lpanel.service" ]]
if grep -q '^docker ' "$fixture/calls"; then
  echo '仅卸载面板时不应调用 Docker' >&2
  exit 1
fi
: > "$fixture/calls"
uninstall ''
[[ ! -s "$fixture/calls" ]]

setup
rm -- "$command_link"
ln -s "$fixture/outside/content" "$command_link"
uninstall $'\ny\n'
[[ "$(readlink "$command_link")" == "$fixture/outside/content" ]]

setup
if uninstall $'y\ny\n' compose; then exit 1; fi
[[ -f "$project/data/content" && -f "$config" && -L "$root/current" ]]
uninstall $'y\ny\n'
grep -Fq -- "$project" "$fixture/output"
[[ ! -e "$project" && -d "$root/data/projects/unregistered" && -f "$fixture/outside/content" && -f "$root/data/acme/private-key" ]]
python3 - "$root/data/panel.db" <<'PY'
import sqlite3
import sys

with sqlite3.connect(sys.argv[1]) as db:
    assert db.execute("SELECT count(*) FROM projects").fetchone() == (0,)
    assert db.execute("SELECT json_extract(payload, '$.kind') FROM bindings").fetchall() == [("site",)]
PY

setup
python3 - "$root/data/panel.db" <<'PY'
import sqlite3
import sys

with sqlite3.connect(sys.argv[1]) as db:
    db.execute("CREATE TRIGGER reject_delete BEFORE DELETE ON projects BEGIN SELECT RAISE(ABORT, '测试拒绝删除'); END")
PY
if uninstall $'y\ny\n'; then exit 1; fi
[[ -f "$project/data/content" && -f "$project/compose.yaml" && -L "$root/current" ]]

setup
mv -- "$root/data/projects" "$fixture/linked-projects"
ln -s "$fixture/linked-projects" "$root/data/projects"
if uninstall $'y\ny\n'; then exit 1; fi
[[ ! -s "$fixture/calls" && -f "$project/data/content" ]]

for failure in config reload; do
  setup
  if uninstall $'n\ny\n' "$failure"; then exit 1; fi
  [[ -L "$root/current" && -f "$fixture/units/lpanel.service" ]]
  grep -Fxq '# 本文件由 LPanel 管理' "$config"
done

setup
echo '# 自定义配置' > "$config"
if uninstall $'n\ny\n'; then exit 1; fi
[[ ! -s "$fixture/calls" && -f "$config" ]]
echo '卸载验证通过：取消、默认保留、Compose 清理与失败重试、Nginx 恢复和非面板配置保留。'
