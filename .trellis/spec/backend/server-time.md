# 宿主机时区与时间同步

时间接口沿用管理员会话。所有路径均位于 `/api/v1/server/time`。

| 请求 | 契约 |
| --- | --- |
| `GET` | `server_time` 含服务器当前时区偏移，`timezone` 为系统时区。浏览器直接呈现返回的当地时间。 |
| `GET /zones` | 返回系统支持的时区列表 `zones`。 |
| `PUT /timezone` | `{"timezone":"Asia/Shanghai"}`。仅接受列表内的时区，设置后重新读取。 |
| `PUT /auto` | `{"enabled":true}` 或 `{"enabled":false}`。设置后返回服务器状态。 |
| `POST /sync` | 返回 HTTP 202 和后台操作。通过 `GET /api/v1/operations/{id}` 查询本次校时结果。 |

`auto_sync` 表示自动校时开关，`synchronized` 表示系统是否已确认同步，未知值返回 `null`。页面依据 `can_set_auto` 和 `can_sync_now` 启用对应操作。`timedatectl show` 在 Ubuntu 24.04 上支持读取全部属性或逐项传入 `-p`，逗号分隔的多属性参数可能成功退出却输出为空。

单次校时使用 systemd-timesyncd。服务原先运行时重新启动，原先停用时临时启动并在结束后停止。临时运行期间，Ubuntu 24.04 的 `timedatectl` 会报告 `NTP=yes`。该服务的持久开关依据 `systemctl show -p UnitFileState` 读取，因此在临时运行期间重新打开页面，或停止临时服务失败后刷新，都能看到实际持久设置。使用其他后端时沿用 `timedatectl` 的状态。重复校时或同时写入自动开关返回 HTTP 409。

本次成功要求 `/run/systemd/timesync/synchronized` 新建或更新，且系统报告 `NTPSynchronized=yes`。校时可能向过去跳变，判断标记变化时需允许修改时间向前或向后移动。校时源不可达、超时和临时服务停止失败须反馈到操作记录。[systemd 同步标记说明](https://www.freedesktop.org/software/systemd/man/latest/systemd-timesyncd.service.html#%2Frun%2Fsystemd%2Ftimesync%2Fsynchronized)

| 输入或条件 | 验证点 |
| --- | --- |
| 服务器更换为 `Asia/Kathmandu` | 返回的时间偏移为 `+05:45`，浏览器更换时区后仍显示服务器当地时间。 |
| 时区不在列表内，或请求缺少 `enabled` | 返回 HTTP 400，原设置保留。 |
| 系统已有同步记录，但本次收不到时间源响应 | 操作以失败结束；已关闭的自动开关在等待和清理完成后均显示关闭。 |
