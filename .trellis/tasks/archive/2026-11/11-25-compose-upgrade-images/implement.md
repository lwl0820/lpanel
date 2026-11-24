# 实施与验证

1. 在 `internal/panel/projects.go` 的项目动作处理链增加镜像升级，接入现有后台操作、项目状态读取与域名同步。核实项目的 Compose 接口对强制拉取和按服务重建的支持。
2. 在 `web/src/views/ProjectsView.vue` 的项目行菜单增加「升级镜像」并沿用已有进度提示。核对 `web/src/projects-api.ts` 的动作参数类型。
3. 为升级动作补充回归测试，覆盖缓存同标签镜像、已拉取但容器仍使用旧镜像、无变化、项目全部或部分停止、拉取失败和动态端口绑定。检查命令目标与容器状态，确保不会对空服务列表执行整项目部署。
4. 运行 `go test ./internal/panel -run 'TestProject.*(Upgrade|Binding)'`，在 `web/` 运行 `npm run typecheck`。通过项目列表验证菜单入口、后台结果与状态刷新。审查修改后同步编排操作前后端契约。
