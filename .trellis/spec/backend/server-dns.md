# 服务器默认 DNS

管理员接口只接受整机默认 DNS 地址列表。

| 请求 | 成功结果 |
| --- | --- |
| `GET /api/v1/server/dns` | 返回 `servers`、`can_edit`、`can_restore` 和可选的中文 `message`。静态配置通过 `max_servers: 3` 告知页面实际地址数上限。 |
| `PUT /api/v1/server/dns` | 接受 `{"servers":["192.0.2.11","2001:db8::11"]}`，写入并回读实际结果后返回状态。 |
| `POST /api/v1/server/dns/restore` | 恢复首次修改前的配置，成功后清除备份，返回新的状态。 |

静态模式只修改管理员维护的 `/etc/resolv.conf` 中的 `nameserver`。保留搜索域、其他选项和权限。生成文件和未识别的符号链接只读。首次备份存在面板私有数据目录，连续保存和面板重启均须保留恢复入口。

使用 systemd-resolved 本地代理时，写面板专用的全局配置片段并读取运行中的解析设置。全局 `DNS=` 先清空原列表，`Domains=~.` 提供默认路由。更具体的网卡域优先匹配。若某条网卡也声明了根域路由，系统可能把查询同时发给多个上游，此时面板提示限制。读取默认地址时保留上游和专用域的区别。[systemd 域名路由规则](https://www.freedesktop.org/software/systemd/man/latest/systemd-resolved.service.html#Protocols%20and%20Routing)

空列表、无效地址和超出对应配置上限的输入返回 HTTP 400。只读配置拒绝保存，应用或回读失败须反馈原因并重新获取实际状态。回归应覆盖连续保存后的恢复、默认上游与专用域并存、保存失败后的恢复入口。系统验收使用有独立应答的测试 DNS，通过 `getent` 等默认解析路径验证保存、重启和恢复。
