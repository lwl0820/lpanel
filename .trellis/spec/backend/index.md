# 后端开发规范

## 开发前检查
1. 阅读当前任务 prd.md、design.md、implement.md 及研究清单。
2. 阅读[基础运行契约](./runtime-contract.md)与[托管和升级契约](./hosting-contract.md)，以实际 API、CLI 和目录规则接入。
3. 会话、文档和注释使用中文；遵循用户要求，保持实现简单。
4. 新模块的运行契约与实际代码不同步时先更新规范，不把模板文字当成已实现行为。

## 规范目录
| 文档 | 状态 |
| --- | --- |
| [基础运行契约](./runtime-contract.md) | 已从代码落实 |
| [网站、项目、证书与升级](./hosting-contract.md) | 已从代码落实 |
| [目录结构](./directory-structure.md) | 初始化模板，按后续真实代码补齐 |
| [数据库](./database-guidelines.md) | 初始化模板，当前以基础运行契约为准 |
| [错误处理](./error-handling.md) | 初始化模板，当前以基础运行契约为准 |
| [质量要求](./quality-guidelines.md) | 初始化模板，检查要求见下方 |
| [日志](./logging-guidelines.md) | 初始化模板，禁止输出账号秘密 |

## 质量检查
运行 go test ./cmd/... ./internal/... 与 go vet ./cmd/... ./internal/...；跨层改动同时运行前端类型检查、lint、测试和构建。真实系统能力以隔离环境运行结果验收，命令模拟不能冒充安装或证书成功。所有缓存和临时文件放仓库 .tmp。
