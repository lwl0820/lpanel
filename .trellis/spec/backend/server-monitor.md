# 服务器资源监控

四个只读接口都使用管理员会话，前缀为 `/api/v1/server/monitor`。采样时间 `at` 使用 Unix 秒，容量为字节，读写速率为字节/秒。

| 请求 | 响应 |
| --- | --- |
| `GET /summary` | `at`、`cpu_percent`、`cpu_model`、`cpu_core_count`、`cpu_logical_count`、`load_average`、`memory_used_bytes`、`memory_total_bytes` 和系统盘 `root_disk`。 |
| `GET /disks` | `{at,disks}`。每项含 `mount`、`used_bytes`、`total_bytes`。 |
| `GET /processes` | `{at,processes}`。每个进程含 `pid`、`name`、`rss_bytes`、`cpu_percent`、`read_bps` 和 `write_bps`。 |
| `GET /history?from=…&to=…` | `{samples}`，按实际采样时间升序返回整机 CPU 使用率、系统负载、内存与各磁盘。使用 Unix 秒起止值，落在保留范围之外的时段裁剪，无匹配样本返回空数组。 |

实时和历史的 `load_average` 都按 1、5、15 分钟排列，数值为系统平均负载。升级前的样本及缺失的负载读数为 `null`。硬件信息仅随实时摘要返回，物理核心数无法确定时为 `null`。

整机内存用总量减去可用量，磁盘按本地挂载点计算。整机 CPU 按相邻 CPU 时间增量求占比，避免重复计入 guest 时间。进程 CPU 以一颗逻辑核满载为 100%，常驻内存按系统页大小换算。读写速率取进程实际存储读写的累计字节差值。进程号相同但启动时间不同，视为新进程。累计计数缺失或只有一个读数时返回 `null`。[Linux 内核的进程统计说明](https://docs.kernel.org/filesystems/proc.html)

历史采样仅随 `App.Serve(ctx)` 运行，使用独立的整机 CPU 基线。SQLite 表 `server_monitor_samples` 以采样秒为主键，同秒只保留一行，各磁盘容量随该行一起保存或清理。启动及后续采样时删除严格早于滚动 30 天的记录。进程计数只在实时请求之间暂存在内存。

未登录返回 HTTP 401，历史区间格式错误或起止反向返回 HTTP 400。首页独立加载监控，其他业务数据仍可单独显示。回归测试覆盖首次速率未知、PID 复用、计数回落、保留期边界和同秒覆盖。隔离机验收通过内核读数核对真实负载，关闭浏览器后检验分钟历史，再停止和启动服务核对曲线断档。
