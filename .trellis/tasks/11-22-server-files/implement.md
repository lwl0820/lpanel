# 文件管理实施清单

- [x] 注册鉴权路由、补齐类型化 API，并在 `web/src/router.ts` 与 `web/src/App.vue` 增加服务器管理入口。此子任务不依赖其他子任务。
- [x] 接通目录浏览和文件操作；在隔离网络中用未登记为网站的验收目录核对上传、编辑、重命名、下载和删除，不修改现存系统文件。
- [x] 用仓库 `.tmp` 下的样本验证容易混淆的读写目标，定向运行 `go test ./internal/panel -run '^TestServerFiles'` 及 `npm --prefix web run typecheck`。浏览器核对文件操作与磁盘内容，转入目录后检查实际列表。
- [x] 没有注册文件清理钩子。验收仅创建并处理本次样本；实际覆盖与环境见[验证记录](../../../docs/validation.md)。
