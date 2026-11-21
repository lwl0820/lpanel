# 实现设计

容器启停沿用已登录的项目路由，新增 POST /api/v1/projects/{id}/containers/{containerID}/actions/{action}。动作限 start 和 stop，按现有单容器日志逻辑核对项目归属，再以容器 ID 调用 Docker，成功返回 200。项目详情行根据 Docker 状态显示适用按钮，完成后刷新状态。

镜像页沿用现有导航和表格样式。GET /api/v1/images 返回按完整镜像 ID 分组的列表，每项包括 id、tags（完整名称与标签的数组）和 size。DELETE /api/v1/images/{id} 使用 Docker 普通删除语义；多个标签或被引用等冲突以明确反馈处理。POST /api/v1/images/prune 调用 Docker 未使用镜像清理，包含带标签的闲置镜像。两项写操作只在用户确认后提交，均返回 200 和包含 message 的 JSON 对象，成功或失败后重新读取列表。所有接口使用现有登录校验。无需数据库迁移。
