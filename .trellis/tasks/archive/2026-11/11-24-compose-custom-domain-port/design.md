# 技术设计

## 项目设置

- web/src/views/ProjectsView.vue:429 当前仅展示扫描结果。编辑器分别维护扫描结果与自定义端口草稿，保存后端口才成为可绑定的候选。保留现有项目保存与部署流程。
- 扩展 web/src/projects-api.ts:3 对应的项目创建、详情和更新契约，新增整数数组 custom_ports。SQLite 项目记录保存列表，与项目其他字段一起写入。旧项目默认空列表，旧客户端更新省略该字段时保留既有列表，显式空数组表示清空。Compose 文件仅描述容器运行配置。
- internal/panel/project_archive.go 的 project.json 元数据携带可选列表，导入沿用项目端口校验。核对清单大小限制，确保当前版本导出的有效清单可被同版本导入。

## 绑定与重部署

- web/src/components/BindDomainDialog.vue:28 当前仅生成已发布 TCP 选项。绑定框从项目详情获得手动列表，从扫描接口获得已发布候选，分别显示来源，按来源构造绑定请求。
- internal/panel/projects.go:897 的项目绑定接口增加宿主机端口参数 host_port，与现有 service 和 container_port 两种目标表达互斥。后端验证该端口属于当前项目，再使用选定协议和 127.0.0.1 生成上游。手动路径无需 Docker 提供端口映射，证书与 Nginx 生效流程复用现有实现。
- 在绑定 JSON 中保存 host_port，以区分固定上游与 Docker 映射。缺少此字段的已发布绑定按原契约处理。internal/panel/projects.go:822 刷新上游时只为已发布绑定重新解析 Docker 映射。域名编辑保留绑定目标类型。项目端口列表是新建绑定的候选，已有绑定持有自己的固定目标。

## 使用条件

Nginx 必须能通过本机 IPv4 回环地址连接该服务。界面在添加端口处说明这一点。项目迁移只带走候选列表，需在新服务器上另行绑定域名。
