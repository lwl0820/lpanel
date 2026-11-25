# HTTP/2 接入设计

## 配置生成

复用 `applyNginxBindings` 已有的 `nginx -V` 调用，从其输出读取版本与 HTTP/2 模块信息，将结果传给 `renderNginx`。版本低于 1.25.1 时，在对应 TLS 监听上使用 `listen ... ssl http2`。从 1.25.1 开始，在相应的 `server` 块按偏好使用 `http2 on;` 或 `http2 off;`。只为证书状态已启用的 `project` 和 `program` 绑定生成配置。`proxy_pass`、上游 `proxy_http_version 1.1` 和 WebSocket 转发均沿用原配置。 [Nginx HTTP/2 模块文档](https://nginx.org/en/docs/http/ngx_http_v2_module.html) · [Nginx 1.25.1 变更公告](https://mailman.nginx.org/pipermail/nginx-announce/2023/BYSVLPUZESCZHJMTDD25QD7ZKZYADAR2.html)

## 绑定设置

编排项目创建、编辑及程序绑定保存接口接收可选布尔字段 `http2`。偏好随绑定保存到 SQLite 的 JSON 字段。新建和旧数据省略字段时按开启处理，编辑省略字段时沿用当前偏好。认证后的 `GET /api/v1/bindings/http2` 返回 `mode`，取值为 `per_domain`、`shared` 或 `unavailable`。共用绑定弹窗仅在 `per_domain` 允许编辑并提交该字段。

## 兼容性

旧版 Nginx 的 HTTP/2 设置作用于共享监听套接字。部署在同一个地址和端口的其他 HTTPS 站点也可能协商到 HTTP/2，仍可通过 HTTP/1.1 访问。用户选择旧版默认开启，升级后允许单独关闭。`shared` 下保留已保存的偏好，等再次使用支持逐域名设置的版本时生效。 [Nginx 共享监听说明](https://trac.nginx.org/nginx/ticket/1034)

使用自编译且未包含 HTTP/2 模块的 Nginx 时，写入 HTTP/2 指令会导致 `nginx -t` 失败。标准安装从 Debian 或 Ubuntu 软件源安装 Nginx，这个问题主要涉及自行替换过的二进制。建议复用这次 `nginx -V` 的输出，只在确认版本和模块可用时生成 HTTP/2 指令，额外逻辑限于解析和一次选择，不增加命令调用或重试。其他情况继续生成原有 HTTPS 配置。

## 应用与恢复

新启用证书的绑定从现有申请证书流程取得配置。已有绑定使用 `setup-nginx` 重新生成，复用已保存的绑定和证书。继续使用 `nginx -t`、重载和配置标识回读确认生效，失败时沿用现有恢复旧配置和绑定的流程。SQLite 表结构沿用原样。
