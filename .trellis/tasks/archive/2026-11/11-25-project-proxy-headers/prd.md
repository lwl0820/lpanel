# 补全编排项目反代请求头

## 目标

编排项目通过域名接入时，上游应用能读取 Nginx 确认的客户端地址，并能在同一入口处理普通 HTTP 和 WebSocket 握手。在线安装服务以 X-Real-IP 核对授权，其他应用也可从 X-Forwarded-For 读取同一地址。

## 范围

沿用本次讨论确定的公网直连接入方式。由编排项目的代理位置覆盖两个来源头，其值均取 $remote_addr。普通请求发往上游时使用 Connection: close，带 Upgrade 的请求保留升级协议并使用 Connection: upgrade。已有项目可用 setup-nginx 重新生成生效配置。本次调整限于编排项目反代和对应的验证、部署说明。按需配置的端口、缓存、HSTS、旧应用专用头和上游 TLS 参数留给各站点处理。

## 验收

- 普通请求经过代理后，即使自带来源头，上游收到的两个来源头都只有 Nginx 识别出的来源地址，且 Connection 为 close。
- WebSocket 客户端能经同一入口完成 101 握手，转发后的 Upgrade 与 Connection 正确。
- 生成的配置能够通过 Nginx 配置检查，多个编排项目共用升级映射。现有绑定重新生成后使用相同行为。

## 本地验收记录

在隔离目录中保存两个编排域名绑定，调用 ApplyNginx 从已保存的绑定重新生成 lpanel.conf。使用 Nginx 1.24.0 加载该文件，测试时将监听端口改为本机随机端口。nginx -t 通过。

两个域名分别从 127.0.0.2 发起普通请求。在请求省略来源头及自带其他来源地址的两种情况下，上游收到的 X-Real-IP 和 X-Forwarded-For 均为单值 127.0.0.2，Connection 为 close。随后两个域名的 WebSocket 握手均返回 101，已核对服务端的 Sec-WebSocket-Accept。
