# 前端 API 与页面契约

## 1. 范围与触发
适用于 web/src/api.ts、App.vue 及后续业务页面。规范仅描述已有 Vue 3、TypeScript、Element Plus 基础，不承诺尚未完成的模块。

## 2. 签名
`request<T>(path, options?)` 默认调用 `/api/v1`。`auth.me`、`auth.login` 与 `auth.logout` 对应后端账号接口。启用入口时，`auth.login` 用登录页提供的同源路径提交。`APIError` 保存 `status`、`code` 和 `message`，页面显示中文 `message`。

访问设置返回 `login_path`。`PUT /api/v1/system/access/login-path` 提交 `{"login_path":"/abcdef"}`，并用返回的配置刷新完整登录地址。空值表示根地址。

`sites.create` 提交网站字段、`hostnames` 数组和可选的证书选项，返回网站、`bindings` 数组与可选的证书操作。`sites.update` 提交 `identifier`、`note`、`directory` 和 `spa`。

`projects.create` 和 `projects.update` 都返回顶层项目字段及部署的 `operation`。项目保存成功但未能启动部署时返回 `deployment_error`。

容器项目信息包含服务端提供的 `directory`。用 `projects.status` 读取容器状态并用同一结果渲染项目状态和展开内容。单容器日志用 `projects.containerLogs` 请求容器 `ID` 对应的记录，项目汇总日志使用 `projects.logs`。

单容器启停通过 `projects.containerAction` 使用当前行的容器 ID，操作结束后重新读取项目状态。镜像页按完整镜像 ID 展示每组标签。删除和清理均需确认，对话框默认聚焦「取消」。成功或失败后刷新列表，失败时展示接口返回的原因。

证书账户由 `GET /api/v1/acme/accounts` 提供，返回账户列表、提供商信息和默认账户 ID。绑定域名、新建静态站和手动签发均用 `acme_account_id` 提交本次选择。绑定返回的 `certificate_account_id` 用于展示实际签发账户。

## 3. 数据与状态
成功直接返回后端 JSON，无 data 包装；错误读取 error.code 和 error.message。fetch 发送 same-origin Cookie。User 只有 username；Operation 包含 id/kind/status/log/error/created_at/updated_at。局部状态使用 ref/computed，不引入没有共享需求的状态框架。

新建表单使用多行输入，每行一个主机名，至少填写一个。未手动修改的网站标识取第一个主机名。目录留空时，列表显示由服务端生成的路径。存在普通域名时可选自动申请 HTTPS，通配符绑定以文本和 HTTP 状态显示。证书操作期间使用创建响应中的绑定，操作完成后刷新各域名的实际状态。

共用账户选择优先预选当前证书的签发账户，新申请预选默认账户。没有默认账户时提示前往「ACME 账户管理」添加账户。账户绑定密钥在表单中以密码输入处理，列表、编辑初始值和账户响应中均不回显。

添加证书账户时，提供商下拉项来自接口的 `providers`。切换时清除凭据和条款勾选，保留已填联系邮箱。ZeroSSL 未完成配置时默认使用邮箱注册，可切换为手动填写绑定凭据。Google Cloud Public CA 未完成配置时显示两项绑定凭据与获取说明。

## 4. 校验与错误矩阵
常规模式下，未登录返回 401 后显示登录页。定制登录页由服务端注入 `lpanel-login-path` 元数据，只有存在该标记的页面才将 `/auth/me` 的 404 当作待登录，普通匿名访问在 Nginx 和 Go 侧直接返回 404。网络或其他服务错误显示中文错误，不能当成已登录或操作成功。网络连接失败使用 `APIError(0, network_error)`，Nginx 返回 HTML 等非 JSON 内容使用 `invalid_response` 并保留 HTTP 状态。表单提交期间显示 loading，成功后清理密码。长操作需等待操作结果再显示成功，不能把请求提交成功当成完成。

建站时主机名为空，提交时定位到主机名输入。绑定校验或生效失败，在弹窗保留填写项。返回 `201` 且带 `certificate_error` 时，显示已创建的 HTTP 网站和重试说明。后台操作有失败时通过 `OperationBanner` 显示原因，并提供域名与证书入口。

点击「创建」或「保存配置」后，配置校验失败时保留表单。保存成功后关闭抽屉并刷新项目列表，通过 `OperationBanner` 显示部署进度和执行失败原因。收到 `deployment_error` 时提示点击「编辑」并重新保存。

项目状态单独加载，状态读取失败可从展开内容看到原因。列表刷新和项目操作结束时重新读取状态。删除确认框每次打开时默认保留项目文件。勾选「同时删除项目文件」时显示目录与删除范围，确认后才提交 `remove_files=true`。

账户删除返回 `409` 时显示接口原因，并保留列表中的默认标记。申请失败时继续显示原证书和原账户，切换成功并刷新后显示新账户。

新增账户收到 `needs_eab: true` 时保留对话框并切换为编辑该账户，提示管理员重试。ZeroSSL 邮箱注册失败时可切换为手动填写。只有保存成功且无需补充注册时才显示完成。

## 5. 正常、基础与失败案例
正常：登录后显示真实管理员与版本。定制入口登录成功后访问 `/`，站内路由直接使用原路径。退出时重新请求 `/`，由服务端决定显示常规登录页或返回 404。
基础：列表为空使用空状态，不能填充虚构项目或统计值。
失败：APIError 中文消息可见；不在产品页面显示内部需求、任务阶段或实现说明。

## 6. 必需测试
web/src/api.test.ts 验证请求、结构化错误、Nginx HTML 错误页和网络中断。Element Plus 在 main.ts 设置 zhCn locale，包含内建按钮和辅助文字。业务页接入时对关键错误/操作状态做有意义测试；运行 npm --prefix web run typecheck、lint、test -- --run、build。测试缓存和其他临时产物放仓库 .tmp；构建 dist 为忽略的正式构建产物。

登录相关测试覆盖旧登录接口、定制入口提交地址及首屏路由保持。浏览器验收同时核对更新后的完整登录地址与普通页面访问。

建站交互核对多行主机名、自动标识和手动覆盖、备注保存与清空、冲突反馈，以及混合普通域名和通配符时的证书结果。

## 7. 错误与正确用法
错误：在每个页面重复 fetch 和错误解析，或者显示假数据让空页面看起来已完成。
正确：共用 request 与 API 类型，业务组件按真实接口状态渲染。仅复用实际存在的共享逻辑，避免提前抽象单次表单或工具函数。
