# 防火墙实施清单

- [x] 已接入服务器管理菜单、状态、启停和单端口规则页面；在 Ubuntu UFW 上核对了停用时的持久化规则与启用后的顺序。
- [x] 已运行 `go test ./internal/panel -run '^TestServerFirewall'` 和 `npm --prefix web run typecheck`，覆盖非默认端口、已有放行和优先拒绝。
- [x] 独立客户端通过新连接核对面板、SSH 以及受限来源，见 `docs/validation.md` 的「本机防火墙」。
- [x] 停用后已通过新请求核对状态和连接。各场景使用一次性副本，结束即销毁测试网络；未在开发宿主机上启停。
