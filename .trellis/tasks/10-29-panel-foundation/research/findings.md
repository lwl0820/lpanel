# 研究：Compose、Nginx、Certbot 与独立升级进程的运行契约

- 查询：核实端口规范化、运行状态、证书与升级命令的实际接口，供后续子任务直接使用。
- 范围：本机只读实测及外部官方资料；不创建容器、不改业务代码、不安装宿主软件。
- 日期：2026-10-29。

## 发现的文件与相关规范

- `internal/panel/config.go:11`：配置包含内部监听地址、面板端口、ACME 参数及 Nginx 配置目录。
- `internal/panel/config.go:35`：配置持久化目录 `data/` 使用 0700 权限。
- `internal/panel/app.go:34`：初始化持久化与命令执行；同包后续模块可复用 App。
- `internal/panel/app.go:47`：外部命令通过参数数组执行，工作目录为安装根，TMPDIR 指向根目录 `.tmp`。
- `.trellis/spec/backend/index.md`：仍为规范模板，未给出既有 Compose/证书约定。
- `.trellis/spec/guides/cross-layer-thinking-guide.md`：应校验配置、API、运行时和页面之间的数据契约，不能只核对一个局部层。

以上代码处于并行实现中，行号可能变化；以名称和实际文件为准。

## Compose 配置实测

本机 CLI 为 Docker Compose 5.6.1。使用标准输入运行 `docker compose --project-name lpanel-research --project-directory /home/user/projects/lpanel -f - config --format json`，无需 daemon、无需临时 YAML 文件。

输入摘要：

```yaml
services:
  web:
    image: nginx:alpine
    ports:
      - "${SERVICE_PORT}:80"
      - "127.0.0.1:18081:81/tcp"
      - "82"
      - target: 83
        published: "18083"
        host_ip: "127.0.0.1"
        protocol: tcp
      - target: 84
        published: "18084-18085"
        protocol: tcp
      - "18053:53/udp"
    expose:
      - "90"
      - "91/udp"
      - "92-93"
```

设置 `SERVICE_PORT=18080` 后，实际规范化要点如下：

- `services` 为服务名到对象的映射。
- `ports[].target` 为 JSON 数字；`published` 为字符串，不能声明为整数。
- `host_ip` 未指定时字段省略；默认 `protocol` 为 `tcp`，还会出现 `mode` 字段。
- 仅写 `"82"` 时，规范化结果有 `target:82`，但没有 `published`，仍然属于要求 Docker 动态发布的 ports，并不是 expose-only。
- 显式 `published:"0"` 实测保留字符串 `"0"`；范围 `published:"18084-18085"` 也保留。两者都必须部署后读取实际端口，不能直接拼成 Nginx 上游。
- `expose` 仍是字符串数组，包含协议后缀和范围；不能期待 CLI 自动变成数字列表。
- 短格式 `"18090-18091:90-91"` 实测展开为两个 ports 项。
- IPv6 短格式 `"[::1]:18092:92"` 实测规范化为 `host_ip:"::1"`，拼接上游应使用 `net.JoinHostPort`，避免丢掉 IPv6 方括号。

[官方 Compose config 说明](https://docs.docker.com/reference/cli/docker/compose/config/)确认规范化会处理变量、文件合并及短格式展开，但业务仍需按结果字段的真实类型解码。

## Compose 运行状态与实际端口

[官方 ps 说明](https://docs.docker.com/reference/cli/docker/compose/ps/)当前明确：`ps --format json` 是 JSON Lines。状态字段包括 `Service`、`State`、`Health`、`ExitCode`；`Publishers` 可为 null，或者包含 `URL`、`TargetPort`、`PublishedPort`、`Protocol` 的数组。查询停止的容器需要 `--all`。

本机对不存在的研究项目只读执行 `ps --all --format json`，退出码 0，stdout 为空字节，不是 `[]`。建议使用 json.Decoder 连续解码 JSON 对象到 EOF；空输入返回空结果。若目标旧 Compose 版本确实返回数组，再基于兼容矩阵处理数组格式，不要假定单一整体数组。

[官方 port 说明](https://docs.docker.com/reference/cli/docker/compose/port/)提供服务与容器端口查询，并可指定 `--protocol` 与副本 `--index`。本轮没有创建容器，因此未实际验证非空 port 输出，不把文档样例冒充运行结果。首版可直接使用 ps 的实际 Publishers，匹配服务、容器端口及协议；多个发布项不能任意取一个错误目标。

## Nginx 检查与证书权限

[官方命令行说明](https://nginx.org/en/docs/switches.html)确认 `-t` 除语法外还尝试打开配置所引用的文件；因此候选 HTTPS 配置的证书路径必须已存在且可读。`-c` 和 `-p` 可以为隔离测试指定独立配置及前缀；`-s reload` 重载运行中主进程。

具体权限边界：当前 App 把 data 建为 0700，不能把由低权限 Nginx worker 提供的 HTTP-01 webroot 放在该目录下面。建议安装根下使用独立可遍历的 `acme-webroot/`，私钥继续位于私有 `data/acme/`。Nginx master 读取证书与 worker 读取验证 token 是两条路径。同理发布前端目录及其父目录必须允许 worker 遍历，不能用整个安装根 0700 后期待静态文件正常访问。

## Certbot 目录与续期契约

[Certbot 官方用户指南](https://eff-certbot.readthedocs.io/en/stable/using.html)支持 `certonly --webroot`；申请、续期应显式共用 `--config-dir`、`--work-dir`、`--logs-dir`，否则读取的是另一套证书状态。设置固定 `--cert-name` 可使引用路径稳定；Nginx 应引用 live 中的证书与私钥路径。

部署钩子 `--deploy-hook` 仅在签发或续期成功后执行；不能仅凭 renew 返回 0 判断证书确已更新，因为无证书需要续期也返回 0。钩子失败不一定让 Certbot 整体返回非零，因此重载错误必须独立记录。默认 dry-run 不运行部署钩子，需要相应测试选项并核对目标系统实际 Certbot 版本能力。现有 timer 的 config/work/log 目录应与申请命令一致。

## systemd-run 独立升级工作契约

[systemd-run 官方说明](https://www.freedesktop.org/software/systemd/man/latest/systemd-run.html)确认默认创建独立 service，由服务管理器作为父进程。升级应使用 service，并设置 `--property=Type=exec`，让启动返回能反映可执行文件成功执行；默认 simple 只保证 fork 成功。

不要使用 `--scope`，也不要使用会等待进程结束的 `--wait`、`--pipe` 或 `--pty`。显式设置工作目录与 TMPDIR，因为独立 service 不继承调用方执行环境。`--collect` 便于清理临时单元，但结果仍需由 worker 持久化，不能将 systemd-run 提交成功当成升级完成。

兼容性：`--working-directory` 从 systemd 240 开始可用，`--collect` 从 236 开始可用；本设计的目标系统可采用。`--expand-environment=no` 到 254 才加入，不能直接依赖其在 Ubuntu 22.04 上可用；固定安装路径避免 dollar 参数展开问题即可，无需引入新兼容层。

## 限制与未找到项

- 本轮没有写代码、启动容器、真实签发证书或运行 systemd 升级。
- 本机没有宿主 Nginx 可执行文件；Nginx 语法及重载结论来自官方文档，后续需在隔离运行环境验收。
- Compose 非空 ps 输出结构来自官方文档；空输出及规范化字段来自本机实测。
- 上述输入样例通过标准输入运行，未在 research 目录以外写临时文件。
