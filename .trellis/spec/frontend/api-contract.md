# 前端 API 与页面契约

## 1. 范围与触发
适用于 web/src/api.ts、App.vue 及后续业务页面。规范仅描述已有 Vue 3、TypeScript、Element Plus 基础，不承诺尚未完成的模块。

## 2. 签名
request<T>(path: string, options?: RequestInit): Promise<T> 统一调用 /api/v1；auth.me/login/logout 对应后端账号接口。APIError 保存 status、code、message，页面显示中文 message。

## 3. 数据与状态
成功直接返回后端 JSON，无 data 包装；错误读取 error.code 和 error.message。fetch 发送 same-origin Cookie。User 只有 username；Operation 包含 id/kind/status/log/error/created_at/updated_at。局部状态使用 ref/computed，不引入没有共享需求的状态框架。

## 4. 校验与错误矩阵
未登录返回 401 后显示登录页；网络或其他服务错误显示中文错误，不能当成已登录或操作成功。网络连接失败使用 APIError(0, network_error)，Nginx 返回 HTML 等非 JSON 内容使用 invalid_response 并保留 HTTP 状态。表单提交期间显示 loading，成功后清理密码。长操作需等待操作结果再显示成功，不能把请求提交成功当成完成。

## 5. 正常、基础与失败案例
正常：登录后显示真实管理员与版本；退出回到登录页。
基础：列表为空使用空状态，不能填充虚构项目或统计值。
失败：APIError 中文消息可见；不在产品页面显示内部需求、任务阶段或实现说明。

## 6. 必需测试
web/src/api.test.ts 验证请求、结构化错误、Nginx HTML 错误页和网络中断。Element Plus 在 main.ts 设置 zhCn locale，包含内建按钮和辅助文字。业务页接入时对关键错误/操作状态做有意义测试；运行 npm --prefix web run typecheck、lint、test -- --run、build。测试缓存和其他临时产物放仓库 .tmp；构建 dist 为忽略的正式构建产物。

## 7. 错误与正确用法
错误：在每个页面重复 fetch 和错误解析，或者显示假数据让空页面看起来已完成。
正确：共用 request 与 API 类型，业务组件按真实接口状态渲染。仅复用实际存在的共享逻辑，避免提前抽象单次表单或工具函数。
