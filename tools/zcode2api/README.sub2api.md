# ZCode 内置平台

来源：[binggao1230/glm-zcode-2api](https://github.com/binggao1230/glm-zcode-2api)，固定版本
`b8d4102c6f24ff1622873fbdbbedddd1e5e7713c`，MIT 许可，纯 Go 标准库零第三方依赖。

Sub2API 把它作为 `zcode` 平台的内部服务一起部署。它把 z.ai / 智谱 GLM Coding Plan 的
Anthropic Messages 通道包装成 OpenAI 兼容接口（`/v1/chat/completions`、`/v1/models`）。

## 与上游的差异（本仓库改造）

- 上游默认从本机 ZCode 桌面配置 `~/.zcode/v2/config.json` 读取凭证，只适合本机运行。
- 本仓库新增 `resolveCredential`：显式配置 `Z2A_UPSTREAM_API_KEY` + `Z2A_UPSTREAM_BASE_URL`
  优先，未配置时才回退桌面配置。因此可作为服务端 sidecar 运行。
- 新增 `/livez` 只报告进程存活，供编排探活；`/healthz` 仍反映上游凭据是否就绪。
- 按编译目标隔离 macOS 的 sysctl 调用；Linux 容器从 procfs 读取内核版本。

## 部署

启用 `deploy/docker-compose.builtin-adapters.yml`，并设置：

- `ZCODE_ADAPTER_KEY`：Sub2API 与 sidecar 之间的共享密钥；252 部署脚本首次部署时自动生成并保存，其他编排方式需配置随机值。
- `ZCODE_UPSTREAM_KEY`：可选。显式指定的 z.ai / 智谱 GLM Coding Plan API key；留空时使用网页授权结果。
- `ZCODE_UPSTREAM_BASE_URL`：可选。默认 `https://open.bigmodel.cn/api/anthropic`；国际版可用
  `https://api.z.ai/api/anthropic`。
- `Z2A_CREDENTIAL_STORE_PATH`：可选。网页授权凭据落盘路径，默认
  `~/.local/state/glm-zcode-2api/credential.json`。

252 集群脚本通过 `SUB2API_BUILTIN_ADAPTERS=1` 启用。后台读取 `BUILTIN_ADAPTER_ZCODE_URL`
与 `BUILTIN_ADAPTER_ZCODE_KEY`，默认内部地址 `http://sub2api-zcode:7865`；端口不发布到公网。

## 调用

添加账号时选择 **ZCode**，类型 `apikey`，然后点击「登录并接入」完成 **网页授权**：
浏览器登录 z.ai / 智谱账号后，把完整回调链接粘贴回来即可。适配器会兑换 Coding Plan
凭据并落盘到凭据存储文件，运行时优先使用它；重新登录会覆盖旧凭据。账号表单无需填写
地址与共享密钥（后端注入）。账号 API Key 是内部共享密钥，不能替代上游凭据。保存时固定
Chat Completions 上游与单并发，创建后自动同步模型目录。

凭据解析顺序：网页授权凭据 → `ZCODE_UPSTREAM_KEY`/`ZCODE_UPSTREAM_BASE_URL` 显式配置 →
本机 `~/.zcode/v2/config.json` 桌面配置。

未配置上游凭据时服务仍可启动，`/livez` 返回 200，`/healthz` 和聊天请求返回 503；
这允许先部署平台，再完成网页授权或配置环境变量。

默认模型 `glm-5.3`；实际支持列表以同步结果为准。多个 ZCode 路由账号共享同一上游通道。

## 验证边界

覆盖平台创建/路由、内置地址注入、协议固定与单并发，以及网页授权的授权码兑换、
凭据落盘和凭据优先级。真实上游调用取决于有效凭证。
