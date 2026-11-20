#!/usr/bin/env bash
set -euo pipefail

fail() { echo "卸载失败：$*" >&2; exit 1; }
if [[ $# -eq 1 && ( "$1" == --help || "$1" == -h ) ]]; then
  echo '用法：sudo bash uninstall.sh。运行后可选择是否同时清理 Compose 项目。'
  exit 0
fi
[[ $# -eq 0 ]] || fail '无需提供参数，请在终端按提示操作'
[[ "$EUID" == 0 ]] || fail '请使用 root 或 sudo 执行'

root=/opt/lpanel
unit_dir=/etc/systemd/system
nginx_conf=/etc/nginx/conf.d/lpanel.conf
database="$root/data/panel.db"
projects_dir="$root/data/projects"
[[ ! -L "$root" ]] || fail "安装目录是符号链接，请手动处理：$root"
[[ ! -e "$root/current" || -L "$root/current" ]] || fail "版本指针不是符号链接，请手动处理：$root/current"

units=()
for unit in lpanel-renew.timer lpanel-renew.service lpanel.service; do
  unit_file="$unit_dir/$unit"
  [[ -e "$unit_file" || -L "$unit_file" ]] || continue
  case "$unit" in
    lpanel-renew.timer) identity='Description=LPanel 自动检查证书续期' ;;
    lpanel-renew.service) identity="ExecStart=$root/current/lpanel renew --root $root" ;;
    lpanel.service) identity="ExecStart=$root/current/lpanel serve --root $root" ;;
  esac
  [[ -f "$unit_file" && ! -L "$unit_file" ]] && grep -Fxq -- "$identity" "$unit_file" \
    || fail "无法确认服务单元属于 LPanel，已保留：$unit_file"
  units+=("$unit")
done

if [[ -e "$nginx_conf" || -L "$nginx_conf" ]]; then
  [[ -f "$nginx_conf" && ! -L "$nginx_conf" && "$(head -n 1 -- "$nginx_conf")" == '# 本文件由 LPanel 管理' ]] \
    || fail "无法确认 Nginx 配置属于 LPanel，已保留：$nginx_conf"
elif [[ ! -L "$root/current" && ${#units[@]} -eq 0 ]]; then
  echo '未检测到已安装的 LPanel，无需卸载。'
  exit 0
fi

echo '卸载后，面板以及通过 LPanel 配置的业务域名将停止访问。'
echo '管理员账号、数据库和证书保留，系统软件包及其他 Nginx 配置保留。'
read -r -p '是否同时清理 LPanel 管理的 Compose 项目？[y/N]：' cleanup || exit 1
project_ids=()
case "$cleanup" in
  y|Y)
    [[ ! -L "$root/data" && ! -L "$projects_dir" ]] || fail '项目数据目录含符号链接，请手动处理'
    echo '以下项目的容器、项目网络和目录将被删除：'
    if ! project_list="$(python3 - "$database" "$projects_dir" <<'PY'
from pathlib import Path
import re
import sqlite3
import sys

database, projects_dir = map(Path, sys.argv[1:])
with Path("/proc/self/mountinfo").open() as mountinfo:
    mount_targets = [line.split()[4] for line in mountinfo]
with sqlite3.connect(f"{database.as_uri()}?mode=ro", uri=True) as db:
    for project_id, name in db.execute("SELECT id, name FROM projects ORDER BY name, id"):
        if not re.fullmatch(r"[0-9a-f]{24}", project_id):
            sys.exit(f"发现无效的面板项目 ID：{project_id!r}")
        project = projects_dir / project_id
        if project.is_symlink() or not all((project / file).is_file() for file in ("compose.yaml", ".env")):
            sys.exit(f"项目 {name!r} 的目录或配置不可用：{project}")
        for mount_target in mount_targets:
            if mount_target == str(project) or mount_target.startswith(f"{project}/"):
                sys.exit(f"项目 {name!r} 下有宿主机挂载点，请先卸载该挂载点：{mount_target}")
        print(f"  {name!r}  {project}", file=sys.stderr)
        print(project_id)
PY
)"; then
      fail '无法读取待清理的 Compose 项目，请按上方提示处理后重试'
    fi
    if [[ -n "$project_list" ]]; then
      mapfile -t project_ids <<< "$project_list"
      echo '项目目录内的数据会永久删除，包括相对路径挂载的数据。'
      echo '未挂载到卷或宿主机的容器内数据也会删除，Docker 卷、镜像和项目目录外的数据保留。'
    else
      echo '  没有已登记的 Compose 项目。'
    fi
    ;;
  ''|n|N) echo '现有 Compose 项目及其文件会保留。' ;;
  *) fail '请在清理选项输入 y 或 n' ;;
esac

if ! read -r -p '确认卸载 LPanel？[y/N]：' confirm || [[ "$confirm" != y && "$confirm" != Y ]]; then
  echo '已取消卸载。'
  exit 0
fi

for unit in "${units[@]}"; do
  if [[ "$unit" == lpanel-renew.service ]]; then
    systemctl stop "$unit" || fail "无法停止 $unit"
  else
    systemctl disable --now "$unit" || fail "无法停用 $unit"
  fi
done

if ((${#project_ids[@]})); then
  if ! python3 - "$database" "$projects_dir" "${project_ids[@]}" <<'PY'
from pathlib import Path
import shutil
import sqlite3
import subprocess
import sys

database, projects_dir = map(Path, sys.argv[1:3])
with sqlite3.connect(f"{database.as_uri()}?mode=rw", uri=True) as db:
    for project_id in sys.argv[3:]:
        project = projects_dir / project_id
        if project.is_symlink():
            sys.exit(f"项目目录是符号链接：{project}")
        try:
            subprocess.run(
                ["docker", "compose", "--project-name", f"lpanel-{project_id}",
                 "--project-directory", str(project), "--env-file", str(project / ".env"),
                 "-f", str(project / "compose.yaml"), "down", "--remove-orphans"],
                cwd=database.parent.parent, check=True,
            )
        except (OSError, subprocess.CalledProcessError) as error:
            sys.exit(f"无法停止并移除项目容器和网络，已保留项目目录 {project}：{error}")
        try:
            db.execute("DELETE FROM bindings WHERE json_extract(payload, '$.kind') = 'project' "
                       "AND json_extract(payload, '$.resource_id') = ?", (project_id,))
            db.execute("DELETE FROM projects WHERE id = ?", (project_id,))
            shutil.rmtree(project)
            db.commit()
        except (OSError, sqlite3.Error) as error:
            sys.exit(f"无法删除项目目录或记录 {project}：{error}")
PY
  then
    fail 'Compose 项目清理未完成，面板程序已保留'
  fi
fi

if [[ -f "$nginx_conf" ]]; then
  mkdir -p -- "$root/.tmp" || fail '无法创建 Nginx 配置备份目录'
  backup_dir="$(mktemp -d -- "$root/.tmp/nginx-uninstall.XXXXXXXX")" || fail '无法创建 Nginx 配置备份目录'
  backup="$backup_dir/lpanel.conf"
  mv -- "$nginx_conf" "$backup" || fail "无法移除 Nginx 配置：$nginx_conf"
  nginx_error=''
  if ! nginx -t; then
    nginx_error='Nginx 配置检查失败'
  elif systemctl is-active --quiet nginx.service && ! systemctl reload nginx.service; then
    nginx_error='Nginx 重载失败'
  fi
  if [[ -n "$nginx_error" ]]; then
    mv -- "$backup" "$nginx_conf" || fail "无法恢复 Nginx 配置，备份位于：$backup"
    rmdir -- "$backup_dir"
    fail "$nginx_error，已恢复原配置，请修复后重试"
  fi
  rm -- "$backup"
  rmdir -- "$backup_dir"
fi

for unit in "${units[@]}"; do
  rm -f -- "$unit_dir/$unit" || fail "无法移除服务单元：$unit"
done
systemctl daemon-reload || fail '无法重新加载 systemd 配置，请修复后重试'
rm -rf -- "$root/releases" || fail '无法删除程序版本目录'
rm -f -- "$root/current" || fail '无法移除运行版本指针'
echo "LPanel 已卸载，保留的面板数据位于 $root/data。"
