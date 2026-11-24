# 技术设计

BindDomainDialog.vue 现有编辑分支直接调用 domains.save，端口和协议控件仅在新建项目绑定时显示。编辑项目绑定时按绑定的 resource_id 读取运行端口和手动候选，将当前上游作为默认目标，从已保存 URL 初始化协议。未选择新目标时独立提交协议，避免候选加载或运行状态阻塞保存。项目之外的绑定沿用原有编辑流程。

为现有项目绑定增加按 ID 更新的入口。后端从数据库获取原绑定并核对所属项目。保留目标时基于数据库现有的地址和端口更新协议；更换目标时复用 bindProject 对 service/container_port、listen_target 和 host_port 的解析，并清除旧目标类型的元数据。沿用原 ID、域名规范化与证书申请规则。继续复用 SaveBindingWithAccount 的保存、Nginx 生效检查和失败回滚；保持同域名有效证书及重部署后上游选择的语义。无需新增持久化字段。
