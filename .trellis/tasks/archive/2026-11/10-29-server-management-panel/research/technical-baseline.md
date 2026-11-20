# 实现依据与技术边界

## 仓库证据
截至规划收敛，仓库没有业务代码、go.mod 或前端业务 package.json。现有前后端规范是初始化模板，不能作为已采用的架构依据。原有规范引导任务不在本次规划中完成或归档。实现阶段从本任务设计建立实际约定，再据真实代码补充规范。

## 已核实的外部契约
- [Compose 配置规范化](https://docs.docker.com/reference/cli/docker/compose/config/)：config 支持 JSON 输出，并处理变量与短格式展开。选择调用官方 CLI，避免自行解释完整 YAML 语义。
- [Compose 服务定义](https://docs.docker.com/reference/compose-file/services/)：ports 发布宿主机端口，expose 描述内部端口。扫描时两者分开呈现；仅 expose 不承诺宿主机 Nginx 能访问。
- [Let’s Encrypt 验证](https://letsencrypt.org/docs/challenge-types/)：HTTP-01 要求公网 80 端口，不支持泛域名。面板自定义 HTTPS 端口不能替代验证端口。
- [Certbot 用户指南](https://eff-certbot.readthedocs.io/en/stable/using.html)：采用 certonly 的 webroot 验证；使用 renew 与部署钩子完成续期后的 Nginx 重载，不让 Certbot 自动改写站点配置。
- [Nginx 控制](https://nginx.org/en/docs/control.html)：配置重载支持平滑切换。生成配置后先检查再重载；失败时恢复面板管理的旧配置，保留原有访问能力。
- [GitHub Releases API](https://docs.github.com/en/rest/releases/releases#get-the-latest-release)：读取公开仓库最新正式发布及其附件下载地址。以发行版本号比较版本，以对应架构的发布附件安装程序，不能把源码压缩包当安装包。
- [Docker Debian 安装要求](https://docs.docker.com/engine/install/debian/)与[Ubuntu 安装要求](https://docs.docker.com/engine/install/ubuntu/)：据此确定系统及架构安装矩阵；复用可用的现有 Docker，不擅自卸载冲突的软件包。
- [modernc SQLite](https://pkg.go.dev/modernc.org/sqlite)：采用无需 C 编译器的 database/sql 驱动，减少两种架构发行包的构建差异。实现时锁定驱动及其配套依赖版本。
- [Element Plus 接入](https://element-plus.org/en-US/guide/quickstart.html)：首版使用直接导入，避免为组件自动导入增加插件。

## 开发边界
- Go 标准库 HTTP、database/sql、SQLite；Vue 3、TypeScript、Vite、Element Plus；不引入 Redis、消息队列、ORM 或通用插件系统。
- 普通命令使用参数数组执行，不将管理员输入拼成 shell 命令；Compose 配置作为文件交给 Compose CLI。
- 临时文件在工作目录 .tmp 内。开发时使用仓库 .tmp，安装与升级时使用安装工作目录 .tmp；不主动访问系统临时目录。
- 采用单管理员登录，不新增多用户或角色体系。标准的密码散列、登录校验和会话 Cookie 属于账号功能本身。
- 复用域名配置和证书逻辑，因为面板、静态网站、Compose 应用均使用它；其他只使用一次且不复杂的逻辑保留在调用处。

## 尚需运行环境验证的事项
文档核实不等于运行验证。真实系统安装、Certbot 测试环境签发、续期后重载、双架构运行、独立升级进程回滚，均列入实施验收；若当前开发环境缺少相应机器或域名，应如实记录未验证项。
