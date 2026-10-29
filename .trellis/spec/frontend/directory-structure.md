# 目录结构

## 概述
前端在 `web/`（Vue 3 + TypeScript + Element Plus，Vite 构建）。页面用 vue-router history 模式，每个页面是 `src/views/` 下按需加载的文件。多个页面共用的界面放 `src/components/`，不含 Vue 状态的逻辑放 `src/` 下的 `.ts` 模块，旁边配 vitest 测试。

## 目录
```
web/src/
├── main.ts              createApp + router + Element Plus（zhCn）
├── App.vue              登录检查、登录卡片、外框（侧栏 / 手机底部标签栏）、<RouterView>、document.title
├── router.ts            路由与页面标题；router.test.ts
├── style.css            :root 设计变量、Element Plus 变量映射、所有共用 class
├── api.ts               request()、auth、Operation 类型
├── *-api.ts             按后端模块封装：projects-api、sites-api、domain-api、upgrade-api
├── operation.ts         waitOperation() 轮询（纯函数，有测试）
├── useOperation.ts      页面内长操作状态，基于 waitOperation
├── cert.ts              证书状态、剩余天数、概览待处理事项（纯函数，有测试）
├── format.ts            时间 / 日期 / 错误文字（纯函数，有测试）
├── icons.ts             AppIcon 使用的 SVG 路径
├── components/          AppIcon、PageHeader、ListState、RowMenu、CertPill、OperationBanner、
│                        BindDomainDialog、LoginCard、AccessPanel、UpgradePanel
└── views/               每个路由一个文件：Overview、Projects、Sites、Domains、Settings
```

## 模块组织
- 新页面：新增 `views/XxxView.vue`，在 `router.ts` 加路由和 `meta.title`，在 `App.vue` 的导航数组加一项。
- 只有一个页面用的对话框、抽屉写在该页面文件里；第二个页面也要用，或页面超过约 250 行时，再移到 `components/`。
- 计算、格式化、轮询等不含 Vue 状态的逻辑写成 `.ts` 模块并配 `*.test.ts`。vitest 在 node 环境运行，这些模块不能引入 `.vue` 文件或 Element Plus。
- 界面改动不修改 `*-api.ts`，它们与后端接口一一对应。

## 命名
- 页面 `XxxView.vue`；公共组件用 PascalCase 名词（`RowMenu.vue`）；组合函数 `useXxx.ts`。
- 移动文件用 `git mv`，保留历史。
