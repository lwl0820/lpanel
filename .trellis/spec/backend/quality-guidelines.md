# 后端验证

先按改动定位 `internal/panel/*_test.go` 中相关用例。修改登录时查鉴权和重启持久化，修改托管时查外部命令结果、配置回退和内容保留，修改 API 时查正常路径和容易出错的响应。遇到已有测试无法覆盖的具体回归，才补最小用例。 [认证测试](../../../internal/panel/app_test.go) · [网站测试](../../../internal/panel/sites_test.go) · [升级测试](../../../internal/panel/upgrade_test.go)

后端常规检查为 `go test ./cmd/... ./internal/...` 与 `go vet ./cmd/... ./internal/...`。聚焦模块时可用 `go test ./internal/panel -run '^TestSite'`。`TestMain` 将测试临时目录放在工作区 `.tmp`。Go 构建和依赖缓存也按 README 放在工作区 `.tmp`。 [开发运行与检查命令](../../../README.md) · [测试初始化](../../../internal/panel/app_test.go)

| 变更 | 对应验证 |
| --- | --- |
| Compose 运行与端口 | 解析和生命周期相关测试。需要真实容器时，在隔离环境运行 `LPANEL_DOCKER_INTEGRATION=1 go test ./internal/panel -run '^TestProjectDockerIntegration$' -v`。 |
| Nginx 与证书 | 核对 HTTP、HTTPS、配置生效标识和失败恢复。代理配置测试、证书替身测试、真实公网签发分别记录。 |
| 安装与卸载 | 用 `bash -n` 检查编辑过的脚本，并执行对应的 `tests/install-smoke.sh` 或 `tests/uninstall-smoke.sh`。真实 systemd 和服务器重启在目标环境验证。 |
| 发布 | 用 actionlint 检查工作流，结合正式发行附件名称、架构、程序版本和 SHA-256 校验。 |

[Compose 集成测试](../../../internal/panel/projects_integration_test.go) · [安装检查](../../../tests/install-smoke.sh) · [卸载检查](../../../tests/uninstall-smoke.sh) · [发布工作流](../../../.github/workflows/release.yml)

## 本地验收与人工验收

AI 执行当前工作环境能够完成的验收。本地缺少公网域名、目标服务器、实机架构或专用环境时，跳过相应的执行步骤，把验收条件和未验证范围记入项目验收记录，标为「待人工验收」。本地用例通过后可以收尾和归档，人工验收项由项目维护者在目标环境执行并补记结果。 [项目验收记录](../../../docs/validation.md#人工验收清单)

记录时分开说明本地实测、命令替身和待人工验收项。测试 CA、交叉编译产物及替身结果只证明已覆盖的路径，公网 CA 和目标架构的状态以对应的人工验收记录为准。
