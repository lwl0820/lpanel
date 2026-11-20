# 前端验证

常规检查从仓库根目录运行：

```bash
npm --prefix web run typecheck
npm --prefix web run lint
npm --prefix web run test -- --run
npm --prefix web run build
```

按改动确定测试范围。修改请求处理时覆盖服务端结构化错误、网络中断和 Nginx 错误页。修改后台操作时覆盖排队、成功、失败和离开页面后取消。证书状态和时间边界沿用已有纯函数测试。 [前端命令](../../../web/package.json) · [API 测试](../../../web/src/api.test.ts) · [轮询测试](../../../web/src/operation.test.ts) · [证书测试](../../../web/src/cert.test.ts)

改动页面交互时，在浏览器核对受影响的成功、失败、空态和加载状态。表单使用明确的中文标签，帮助文字保持可见并关联输入框，提交期间显示状态。列表在窄屏使用项目现有卡片布局。 [组件约定](./component-guidelines.md) · [网站表单与列表](../../../web/src/views/SitesView.vue)

ESLint 与 TypeScript 检查使用现有配置。新增界面文案面向管理员，错误提示应明确当前状态和可执行的操作。生产构建用于发现构建错误，具备条件时在本地隔离环境验证代理、证书和外部服务交互。 [ESLint 配置](../../../web/eslint.config.js) · [前端 API 契约](./api-contract.md)

页面本地可测的交互完成验证后即可收尾。依赖公网、真机或专用环境的交互交由人工验收，记录前置条件与待验证结果。 [项目验收规则](../backend/quality-guidelines.md#本地验收与人工验收)
