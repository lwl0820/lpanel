# 前端开发规范

## 开发前检查
阅读当前任务三份规划文档，再阅读[API 与页面契约](./api-contract.md)。使用 Vue 3、TypeScript、Element Plus；产品界面使用中文，不展示开发需求和内部任务说明。

## 规范目录
| 文档 | 状态 |
| --- | --- |
| [API 与页面契约](./api-contract.md) | 已从代码落实 |
| [目录结构](./directory-structure.md) | 已从代码落实 |
| [组件](./component-guidelines.md) | 已从代码落实，含公共组件和长操作用法 |
| [组合式逻辑](./hook-guidelines.md) | 初始化模板，不要求提前抽取组合函数 |
| [状态管理](./state-management.md) | 初始化模板，当前使用局部响应式状态 |
| [质量](./quality-guidelines.md) | 初始化模板，检查命令见下方 |
| [类型](./type-safety.md) | 初始化模板，以实际 API 类型为准 |

## 质量检查
运行 npm --prefix web run typecheck、lint、test -- --run、build。页面交互需验证加载、空态、错误和完成状态；不能只凭构建成功宣称完整交互验收通过。
