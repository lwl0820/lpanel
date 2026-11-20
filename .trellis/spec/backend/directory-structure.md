# 后端目录结构

| 位置 | 职责 |
| --- | --- |
| `cmd/lpanel/` | 解析 CLI 参数、组装 `panel.App`，执行初始化、服务启动、证书续期和升级工作进程。入口见 [main.go](../../../cmd/lpanel/main.go) 和 [upgrade.go](../../../cmd/lpanel/upgrade.go)。 |
| `internal/panel/` | 后端业务共用 `panel` 包。按功能放在对应文件，路由、业务操作和持久化留在负责模块。 |
| `deploy/` | 安装和卸载脚本、systemd 服务及续期定时器。 |
| `scripts/` | 发行构建脚本。[build-release.sh](../../../scripts/build-release.sh) 将二进制、前端和部署文件打成同一发行包。 |

`App` 提供配置、数据库、HTTP 路由和系统命令边界。`Open` 初始化基础表并调用各个模块的路由注册函数。新建站点与已有站点接口集中在 `sites.go`，域名绑定与 Nginx 生成集中在 `domains.go`。升级检查在 `upgrades.go`，独立工作进程在 `upgrade_worker.go`。 [应用组装](../../../internal/panel/app.go) · [站点路由](../../../internal/panel/sites.go)

模块的 Go 测试放在同目录的 `*_test.go`。部署脚本的分支检查放在 `tests/`。已有命令边界可用 `App.Command` 和 `App.NginxProbe` 替换，业务测试通过真实 SQLite 核对状态。 [域名测试](../../../internal/panel/domains_test.go) · [卸载冒烟](../../../tests/uninstall-smoke.sh)
