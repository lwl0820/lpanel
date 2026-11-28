# 实施顺序

1. 对照 saveProject 的创建输入和 writeProjectDeployment 的三个返回分支，接入创建与部署查询；在隔离测试项目上核对成功、失败及同名冲突。
2. 接入程序管理与日志快照。核对复制结果、缺失程序、依赖未就绪、启动失败及删除行为，验证已有网页接口。
3. 从实际 tools/list 与面板路由复核 docs/mcp-capabilities.md，从 README 的 MCP 说明加入入口。
4. 使用仓库 .tmp 缓存执行相关 Go 测试与 go vet，使用 MCP 客户端验证上述两项工作流并记录测试环境。
