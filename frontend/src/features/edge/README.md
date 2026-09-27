# 边缘服务工作台

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
