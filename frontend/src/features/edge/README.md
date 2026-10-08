# 边缘服务工作台

- `EdgeServiceCatalog.vue`：服务卡片矩阵，支持分类与搜索。
- `catalog.ts`：服务目录和 URL 契约。`service`、`tab`、`endpoint`、`adapter` 可分享；请求和凭据草稿不写入 URL。
- `EdgeServiceDocs.vue`：服务专属端点、请求参数、鉴权和响应文档。
- `TranslateProvidersCard.vue`：翻译适配上下文；服务开关、优先级和脱敏凭据由管理员配置接口保存，运行时立即读取。
- `EdgeServiceContext.vue` / `contexts.ts`：展示视觉、TTS、配音运行配置及 Ark / Laya / JEV 实际账号。部署参数只读；账号密钥通过专用脱敏接口更新，留空保留原值。
- `edge-console.css`：仅作用于边缘服务的控制台样式，包含详情文档、请求示例、上下文及移动端布局。

- `DubbingForm.vue`：视频 File、自动/时间轴模式、JSON 参数导入导出。
- `dubbing.ts`：表单契约及客户端校验；服务端仍做权威校验。
- `DubbingDocs.vue`：两种流程、参数语义、鉴权及任务下载说明。
- `useEdgeResponse.ts`：请求取消、业务失败状态、二进制播放下载和对象 URL 清理。
- `curl.ts` / `codegen.ts`：结构化 multipart 字段同时驱动示例；文件字段与文本字段不能按 `&` 拆分。示例密钥均来自 SUB2API_KEY 环境变量。
- 文案位于 `i18n/locales/{zh,en}/admin/settings.ts` 的 `vision` 节点。

视频配音的唯一参数来源是 DubbingOptions；表单、请求 FormData、各语言示例使用同一对象。
浏览器必须选择真实文件，不能读取导入 cURL 中的本机路径。媒体任务下载仅接受本网关任务路径，
使用发起请求时的认证头，防止将 API Key 发到任意产物 URL。

验证：在 frontend 运行 `node node_modules/vitest/vitest.mjs run src/features/edge/__tests__ src/views/admin/__tests__/VisionEdgeView.spec.ts`，
并执行类型、i18n 检查和构建。服务端契约与命令示例见 `edge-media/dub/README.md`。
