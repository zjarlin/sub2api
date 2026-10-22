# Qoder 签名目录侧车

Qoder 的模型目录接口 `GET https://api2.qoder.sh/algo/api/v2/model/list?Encode=1`
只接受官方客户端签名：

- `Authorization: Bearer COSY.*`
- 全套 `Cosy-*` 头（`Cosy-User`、`Cosy-Client-Type`、`Cosy-Key` 等）
- `/algo` 前缀由签名器加的，不要自己拼

签名逻辑编译在 Rust→WASM 里，Go 侧不引入 WASM 运行时，所以拆成 Node 侧车。

## 文件

| 文件 | 作用 |
| --- | --- |
| `build-wasm-lib.mjs` | 从 Qoder CLI worker runtime 的 `*.obf.mjs` 里抠出 base64 WASM，切成 `wasm-lib.mjs` |
| `wasm-lib.mjs` | 切分结果（约 32.7MB），导出 `__initWasm` / `QoderContext` / `__getWasm` |
| `fetch.mjs` | 用 WASM 生成签名上下文，真实请求目录；导出 `fetchCatalog(req)` |
| `serve.mjs` | 文件队列常驻进程，供生产主容器调用 |

## 协议（文件队列）

主进程（`backend/internal/service/qoder.go` 的 `QoderCatalogSidecarFetcher`）：

1. 先把请求写 `<dir>/req-<uniq>.tmp`，再 `rename` 成 `<dir>/req.json`（原子可见）
2. 轮询 `<dir>/resp.json`，要求 mtime 不早于本次请求写入时刻（防读到旧结果）
3. 读到后删除 `resp.json`，解析 `{ok, body, error}`；`body` 是上游明文 JSON

侧车（`serve.mjs <dir>`）：100ms 轮询 `req.json`，写 `resp-*.tmp` 后 rename 成
`resp.json`，再删除 `req.json`。响应超过 16MB 直接报错。

为什么不用 `docker exec -i ... node fetch.mjs` 管道：`docker exec` 的 stdout 在
64KB 处被截断，而 chat 目录响应约 77KB，JSON 会被切成半截。文件队列也不需要在
主容器里挂 `docker.sock`。

## 请求参数

```json
{"access_token":"dt-...","uid":"...","scene":"chat","machine_id":"可选"}
```

- `machine_id` 缺省时每次随机 UUID（签名上下文的一部分，不影响目录结果）
- `encrypt_user_info` 与 `key` 必须由 WASM 的 `generate_runtime_auth_fields` 生成；
  只放 uid 会被上游 403 `{"code":"101","message":"Signature invalid"}`
- 目录响应是明文 JSON，**不要**走 `decrypt_server_response`

## 升级 Qoder CLI / 换 WASM 版本

1. 从官方下载新 runtime 包，例如：
   `https://download.qoder.com/qodercli/releases/1.1.49/qodercli-worker-runtime-darwin-arm64.tgz`
2. 解包后生成：
   ```bash
   node tools/qoder-catalog/build-wasm-lib.mjs <解包目录>/qoder-worker-runtime/qoder-worker-runtime.obf.mjs \
        tools/qoder-catalog/wasm-lib.mjs
   ```
3. 用真实账号验证：
   ```bash
   # token/uid 从数据库读取，不要写进文件或命令历史之外的任何地方
   ACC=$(docker exec postgres psql -U sub2api -d sub2api -tAc \
     "select credentials->>'access_token' from accounts where id=855")
   UID_=$(docker exec postgres psql -U sub2api -d sub2api -tAc \
     "select credentials->>'uid' from accounts where id=855")
   node -e 'import("./tools/qoder-catalog/fetch.mjs").then(async m=>{const r=await m.fetchCatalog({access_token:process.env.ACC,uid:process.env.UID_});console.log(r.ok, (r.body||r.error||"").slice(0,120))})' 
   ```
   期望 `{"ok":true,...}`，否则会返回 `HTTP 403: ... Signature invalid`。
4. `fetch.mjs` 里硬编码了 `QoderContext` 的客户端版本 `1.1.49`；升级包版本时同步改。

## 部署

`deploy/docker-compose.qoder-catalog.yml` 追加的编排：

- `qoder-catalog`：`node:22-bookworm-slim`，跑 `serve.mjs /var/lib/qoder-catalog`
- `sub2api`：只读挂载 `./tools/qoder-catalog`，共用命名卷 `qoder_catalog_queue`
- 注入 `BUILTIN_ADAPTER_QODER_CATALOG_{SCRIPT,WASM,QUEUE_DIR}`

`deploy/cluster/deploy-252.sh` 存在该文件时会自动追加。不要手工 `docker compose
up sub2api`（详见仓库 AGENTS.md）。
