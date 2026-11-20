# 前端开发规范

开发前阅读当前任务的需求与设计，再查看前端 API、目录和组件约定。项目使用 Vue 3、TypeScript 和 Element Plus。产品文案应使用管理员能直接理解的中文，展示操作入口、结果和可执行的处理方式。

| 文档 | 适用范围 |
| --- | --- |
| [API 与页面契约](./api-contract.md) | 请求、错误、创建网站和后台操作 |
| [目录结构](./directory-structure.md) | 页面、组件和 API 模块的位置 |
| [组件](./component-guidelines.md) | 共享组件、表单、列表与操作提示 |
| [组合式逻辑](./hook-guidelines.md) | 页面状态、操作轮询与取消 |
| [状态管理](./state-management.md) | 登录、局部状态和升级重连 |
| [质量](./quality-guidelines.md) | 类型检查、lint、构建和浏览器验证 |
| [类型](./type-safety.md) | 请求与响应类型、状态和组件事件 |

按改动范围运行类型检查、lint、测试和生产构建。界面改动在浏览器核对受影响的交互和状态。接口与状态契约一起变化时，同步核对 API 模块和页面调用。
