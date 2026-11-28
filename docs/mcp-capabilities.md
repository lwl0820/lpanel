# 面板 MCP 功能清单

已发布的 `v0.1.26` 提供 21 个工具。当前开发版在此基础上增加 11 个，合计 32 个。Agent 连接后应以该服务器返回的 `tools/list` 为准。下表的缺口是面板界面已有、当前 MCP 仍未覆盖的功能。

| 模块 | v0.1.26 已有工具 | 当前开发版新增工具 | 仍有缺口 |
| --- | --- | --- | --- |
| 宿主机文件 | `files_list`、`files_read_text`、`files_write_text`、`files_mkdir`、`files_move`、`files_rename`、`files_delete` | — | 新建文件或上传、分片上传、下载二进制文件、复制、压缩、解压、修改权限和属主。文本写入仅支持已有文件。 |
| Compose 项目与容器 | `projects_list`、`containers_list`、`containers_logs`、`containers_set_state` | `projects_create`、`projects_deployment_operation` | 读取已有项目的完整配置、更新配置、项目级部署及启停、重启、升级镜像、删除、导入导出、扫描配置端口、发现运行端口、项目汇总日志。 |
| 本地镜像 | — | — | 列出、删除和清理未使用镜像。 |
| 宿主机程序 | — | `programs_list`、`programs_get`、`programs_create`、`programs_update`、`programs_copy`、`programs_delete`、`programs_set_state`、`programs_runtime`、`programs_logs` | 安装 Supervisor、持续订阅日志。上传新的程序文件也需要使用面板文件管理。 |
| 静态网站 | `sites_list`、`sites_create`、`sites_update`、`sites_delete` | — | 主要站点工作流已覆盖；发布新网页文件仍需上传能力。 |
| 域名与证书 | `bindings_list`、`bindings_add_site`、`bindings_update`、`bindings_remove`、`certificates_request`、`certificates_operation` | — | 为项目或程序添加反代绑定、更换上游目标、配置 HTTP/2、ACME 账户管理。 |
| 监控与服务器设置 | — | — | 实时资源、历史指标、磁盘与进程、防火墙、交换空间、DNS、时区与时间同步。 |
| 面板管理 | — | — | 通用后台任务查询、版本与升级、访问设置。证书与项目部署各有专用任务查询工具。MCP 开关和令牌签发、撤销由管理员在面板操作。 |

## 判断操作是否完成

- **创建 Compose 项目**。调用 `projects_create`，提供项目名称和 Compose 配置，或使用面板标准项目目录中已准备好的配置。响应中的顶层 `id` 是项目 ID，`operation.id` 是本次部署任务 ID。用后者调用 `projects_deployment_operation`，状态为 `running` 时继续查询；`success` 后可用项目 ID 查看容器，`failed` 时读取 `error` 和 `log`。如果创建响应含 `deployment_error`，项目已保存，但任务未能启动，重复创建同名项目会发生冲突。
- **管理宿主机程序**。调用 `programs_create` 登记程序后，使用返回的程序目录准备入口文件。用 `programs_update` 保存启动设置，再用 `programs_set_state` 启动、停止或重启。用 `programs_get` 查看状态，`programs_runtime` 查看资源与监听端口，`programs_logs` 读取最近日志快照。Supervisor 未就绪时，管理员可在面板的「程序管理」中安装。
