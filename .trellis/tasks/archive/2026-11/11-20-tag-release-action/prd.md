# 标签触发 GitHub Release 自动发布

## 目标

将 Git 标签与正式发行包发布连接起来。维护者推送 `vX.Y.Z` 标签后，GitHub Actions 用该标签对应的源码构建并发布 Release，在线安装和升级可以下载相应版本。

## 已确认范围

- 仓库当前没有 Actions 工作流。复用现有 `scripts/build-release.sh`，沿用 `go.mod` 的 Go 版本和 README 支持的 Node.js 版本。
- 产出 Linux amd64、arm64 发行包及两个同名 `.sha256` 文件。构建成功并校验后创建正式 Release，沿用安装器和升级器识别的附件名称。
- 保留手动输入已有标签的入口。用户已确认将工作流推送到 `main`，并用它补发已存在的 `v0.1.1`。从标签构建，保留其原提交。
- README 说明后续标签发布与已有标签的补跑方式。应用、安装与升级行为继续沿用现有契约。

## 验收

- [x] `vX.Y.Z` 标签推送与指定已有标签的手动运行共用同一构建与发布步骤。检查工作流语法和附件契约。
- [x] GitHub Actions 使用 `v0.1.1` 对应的提交成功发布正式 Release，标签指向保持原值。
- [x] GitHub 匿名接口返回 `v0.1.1` 为最新正式 Release，两个架构的发行包和校验文件均可下载并通过 SHA-256 校验。

## 验收记录

- `actionlint`、Bash 语法检查和发布步骤的参数与校验失败模拟通过。工作流提交 `4ac3eec` 已推送到 `main`。
- [GitHub Actions 补发运行](https://github.com/lwl0820/lpanel/actions/runs/44653096304) 成功。推送前后核对 `v0.1.1` 均指向 `5f3f160`。
- 匿名接口返回 [v0.1.1](https://github.com/lwl0820/lpanel/releases/tag/v0.1.1) 作为最新正式 Release。两个架构的发行包和校验文件可下载，SHA-256 校验通过。检查了二进制架构和 amd64 程序版本。
