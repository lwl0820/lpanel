#!/usr/bin/env bash
set -euo pipefail
project_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$project_root"
version="${1:-}"
if [[ ! "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo '用法：bash scripts/build-release.sh vX.Y.Z' >&2
  exit 1
fi
mkdir -p .tmp/go-cache .tmp/go-mod .tmp/build .tmp/releases .tmp/npm-cache
export TMPDIR="$project_root/.tmp/build"
export GOTMPDIR="$TMPDIR"
export GOCACHE="$project_root/.tmp/go-cache"
export GOMODCACHE="$project_root/.tmp/go-mod"
export npm_config_cache="$project_root/.tmp/npm-cache"
npm --prefix web run build
for architecture in amd64 arm64; do
  stage="$project_root/.tmp/releases/stage-$architecture"
  rm -rf -- "$stage"
  mkdir -p "$stage/web" "$stage/deploy"
  CGO_ENABLED=0 GOOS=linux GOARCH="$architecture" go build -trimpath -ldflags "-s -w -X main.version=$version" -o "$stage/lpanel" ./cmd/lpanel
  cp -R web/dist/. "$stage/web/"
  if [[ -d deploy ]]; then cp -R deploy/. "$stage/deploy/"; fi
  name="lpanel_${version}_linux_${architecture}.tar.gz"
  tar -czf ".tmp/releases/$name" -C "$stage" lpanel web deploy
  (cd .tmp/releases && sha256sum "$name" > "$name.sha256")
done
echo "发行包已生成：$project_root/.tmp/releases"
