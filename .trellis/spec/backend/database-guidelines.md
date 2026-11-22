# 数据库与持久化

面板使用 `database/sql` 和 `modernc.org/sqlite`，数据库位于 `Root/data/panel.db`。`Open` 设置单连接并启用外键检查。`busy_timeout=3000` 让服务与终端命令在短时间写锁冲突时等待。基础表随 `Open` 初始化，业务表由对应模块的注册函数使用 `CREATE TABLE IF NOT EXISTS` 创建。修改表结构时要验证已有安装的数据库仍可打开。 [数据库初始化](../../../internal/panel/app.go) · [项目注册](../../../internal/panel/projects.go)

查询通过 `QueryRowContext(...).Scan(...)` 或 `QueryContext` 接收类型化结果，写入通过 `ExecContext` 和 `?` 参数传值。例如网站详情查询：

```go
err := a.DB.QueryRowContext(ctx,
    "SELECT id,name,directory,spa,note,updated_at FROM sites WHERE id=?", id,
).Scan(&s.ID, &s.Identifier, &s.Directory, &s.SPA, &s.Note, &s.UpdatedAt)
```

列表查询关闭 `rows` 并检查 `rows.Err()`。先读完并关闭结果集再开始下一次数据库操作，避免占用唯一连接。 [站点查询](../../../internal/panel/sites.go) · [域名列表](../../../internal/panel/domains.go)

数据库保存网站元数据、项目配置和操作记录。`projects.directory` 保存相对于 `data/projects/` 的目录名，项目的 `compose.yaml` 和 `.env` 位于该目录根部。首次升级按 ID 回填原路径。域名表的 `domain` 列有唯一约束，`payload` 保存绑定详情，由 `storeBinding` 一起更新。 [项目文件写入](../../../internal/panel/projects.go) · [绑定写入](../../../internal/panel/domains.go)

Nginx 重载与 SQLite 写入是两个操作。变更网站目录或绑定时沿用已有锁和回滚流程，配置应用失败要恢复相应记录与上一次访问配置。回归检查数据库和生成文件两个结果。 [网站回滚测试](../../../internal/panel/sites_test.go) · [绑定回滚测试](../../../internal/panel/domains_test.go)
