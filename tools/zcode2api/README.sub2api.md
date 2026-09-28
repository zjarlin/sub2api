# ZCode 内置平台

来源：[binggao1230/glm-zcode-2api](https://github.com/binggao1230/glm-zcode-2api)，固定版本
`b8d4102c6f24ff1622873fbdbbedddd1e5e7713c`，MIT 许可，纯 Go 标准库零第三方依赖。

Sub2API 把它作为 `zcode` 平台的内部服务一起部署。它把 z.ai / 智谱 GLM Coding Plan 与 Start Plan 的
Anthropic Messages 通道包装成 OpenAI 兼容接口（`/v1/chat/completions`、`/v1/models`）。

## 与上游的差异（本仓库改造）

- 上游默认从本机 ZCode 桌面配置 `~/.zcode/v2/config.json` 读取凭证，只适合本机运行。
- 本仓库新增网页授权与凭据持久化：网页授权结果优先，其次显式配置
  `Z2A_UPSTREAM_API_KEY` + `Z2A_UPSTREAM_BASE_URL`，最后回退桌面 Coding Plan 配置。
- Start Plan 使用 ZCode 登录 JWT、独立端点与每次请求的新验证参数，模型目录来自实际套餐权益。
  浏览器验证由可选 Node 服务承担，Go 模块继续只依赖标准库。
- 新增 `/livez` 只报告进程存活，供编排探活；`/healthz` 仍反映上游凭据是否就绪。
- 按编译目标隔离 macOS 的 sysctl 调用；Linux 容器从 procfs 读取内核版本。

## 部署

启用 `deploy/docker-compose.builtin-adapters.yml`，并设置：

- `ZCODE_ADAPTER_KEY`：Sub2API 与 sidecar 之间的共享密钥；252 部署脚本首次部署时自动生成并保存，其他编排方式需配置随机值。
- `ZCODE_UPSTREAM_KEY`：可选。显式指定的 z.ai / 智谱 GLM Coding Plan API key；留空时使用网页授权结果。
- `ZCODE_UPSTREAM_BASE_URL`：可选。默认 `https://open.bigmodel.cn/api/anthropic`；国际版可用
  `https://api.z.ai/api/anthropic`。
- `ZCODE_UPSTREAM_PLAN`：默认 `coding-plan`，也可设为 `start-plan`。网页授权的套餐选择
  保存在凭据文件中，优先于这个默认值。旧凭据文件没有套餐字段时按 Coding Plan 处理。
- `ZCODE_OAUTH_PROVIDER`：网页授权默认区域，`zai` 或 `bigmodel`；管理页可以覆盖。
- `ZCODE_START_PLAN_VERIFIER_URL` / `ZCODE_START_PLAN_VERIFIER_KEY`：Start Plan 验证服务
  地址与独立共享密钥，Coding Plan 无需配置。
- `Z2A_CREDENTIAL_STORE_PATH`：可选。网页授权凭据落盘路径，默认
  `~/.local/state/glm-zcode-2api/credential.json`。

252 集群脚本通过 `SUB2API_BUILTIN_ADAPTERS=1` 启用。后台读取 `BUILTIN_ADAPTER_ZCODE_URL`
与 `BUILTIN_ADAPTER_ZCODE_KEY`，默认内部地址 `http://sub2api-zcode:7865`；端口不发布到公网。

## 调用

添加账号时选择 **ZCode**，类型 `apikey`，然后点击「登录并接入」完成 **网页授权**：
先选择套餐与账号区域，在浏览器完成登录后等待管理页自动接入。适配器通过官方轮询接口取得所选套餐
凭据并落盘到凭据存储文件，运行时优先使用它；成功重新登录会覆盖旧凭据，失败保留旧凭据。账号表单无需填写
地址与共享密钥（后端注入）。账号 API Key 是内部共享密钥，不能替代上游凭据。保存时固定
Chat Completions 上游与单并发，创建后自动同步模型目录。

OAuth 使用 ZCode 官方 `/api/v1/oauth/cli/init` 与 `/poll/{flow_id}` 流程。授权链接中的
`redirect` / `redirect_uri` 指向带 `app_version` 的官网中转页，由官网完成一次性 code 兑换；
适配器只持有当前会话的随机轮询令牌，不需要浏览器回调链接或桌面应用凭据。

凭据解析顺序：网页授权凭据 → `ZCODE_UPSTREAM_KEY`/`ZCODE_UPSTREAM_BASE_URL` 显式配置 →
本机 `~/.zcode/v2/config.json` 桌面配置。

未配置上游凭据时服务仍可启动，`/livez` 返回 200，`/healthz`、模型目录和聊天请求返回 503；
这允许先部署平台，再完成网页授权或配置环境变量。

多个 ZCode 路由账号共享一个上游登录账号和套餐。切换套餐后应重新同步所有相关账号的模型目录。
同时使用不同套餐或登录账号需要独立适配器实例，不能在同一实例内按路由账号切换。

## Start Plan

管理页选择 **Start Plan** 和实际账号区域。授权完成前会检查
`/api/v1/zcode-plan/billing/balance` 中未过期的 `model:*` 权益；无对应权益时拒绝保存。
`/v1/models` 只返回实际授权且在适配器配置中支持的模型。

请求使用 `https://zcode.z.ai/api/v1/zcode-plan/anthropic`，以登录 JWT 作为 `x-api-key`，
不会改写成 Coding Plan 的 `/api/v1/ultra/anthropic`。JWT 到期或被上游拒绝时返回
`start_plan_reauthorization_required`，需要重新登录；目前没有自动刷新流程。
显式配置时同时设置 `ZCODE_UPSTREAM_PLAN=start-plan`、上述端点和有效登录 JWT，不能使用
Coding Plan API key。Start Plan 不从桌面 provider 配置提取登录 JWT。

每个聊天请求调用 [verifier/README.md](verifier/README.md) 所述服务，使用官方 Aliyun SDK
获取一次新验证参数。验证服务没有上游 JWT。验证浏览器与模型请求应使用相同网络出口。
缺少验证服务返回 503；无头浏览器遇到人工挑战返回 409
`start_plan_interactive_verification_required`，此时须改用可见验证浏览器，由用户完成挑战。
可见模式在当前请求内等待完成，验证码不会保存或重用。

可选无头部署，先配置随机 `ZCODE_START_PLAN_VERIFIER_KEY`，再从仓库根目录执行：

```sh
docker compose --project-directory . --env-file .env \
  -f deploy/docker-compose.yml \
  -f deploy/docker-compose.builtin-adapters.yml \
  -f deploy/docker-compose.zcode-start-plan.yml \
  up -d --build sub2api-zcode sub2api-zcode-verifier
```

已有集群需在自己的完整 Compose 文件组合中追加 Start Plan 叠加文件，并保持原项目名。
该命令只更新两个适配器服务；管理页和登录选项转发还需要发布本仓库对应的前后端版本。
252 集群脚本默认不启动浏览器；选择外部可见验证服务时，直接设置地址和共享密钥即可。
`/livez` 只证明进程存活，`/healthz` 只证明存在凭据，均不证明权益和实际调用成功。

## 验证边界

覆盖平台创建/路由、内置地址注入、协议固定与单并发，以及网页授权的初始化、轮询、
凭据落盘和凭据优先级。Start Plan 测试覆盖 JWT/端点隔离、套餐目录、新验证参数、
失败授权保留旧凭据、交互验证、缺少验证服务和过期登录。
运行 Go 的 `go test ./...` 与验证服务的 `npm test`；真实上游模型调用须单独验收。
