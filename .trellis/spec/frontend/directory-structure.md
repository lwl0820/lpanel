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
- 页面专用的对话框和抽屉留在该页面。多个页面共用的界面，或有独立职责的界面区域，可移到 `components/`。
- 共用的计算、格式化和轮询等纯逻辑放在 `.ts` 模块。Vitest 在 Node.js 环境运行，纯逻辑模块应可独立测试。按实际回归风险补充 `*.test.ts`。
- 界面调整集中在页面和组件。接口变化时同步更新对应的 `*-api.ts` 与调用方。

## 命名
- 页面 `XxxView.vue`；公共组件用 PascalCase 名词（`RowMenu.vue`）；组合函数 `useXxx.ts`。
- 移动文件用 `git mv`，保留历史。
