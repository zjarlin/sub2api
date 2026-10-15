# TeleAgent Server Adapter

TeleAgent 2.7.0 的云端 Chat Completions 适配器。网页登录、令牌保存、请求签名和积分签到均在 Linux 服务器执行，无需常开 Mac、Windows 或 TeleAgent 桌面进程。

## 运行与账号

```sh
npm ci
TELEAGENT_ADAPTER_KEY=<至少24字符的部署密钥> npm start
```

默认监听 `127.0.0.1:7872`。管理端和普通用户复用 `/internal/login/sessions`：`POST` 创建，`POST /<id>/poll` 查询，`GET /<id>/view` 获取 PNG，`POST /<id>/input` 转发鼠标、键盘和文本，`DELETE /<id>` 取消。所有登录接口要求 `Authorization: Bearer <部署密钥>` 和 `X-Login-Owner`，每个会话有独立浏览器上下文及十分钟有效期。用户在 sub2api 的网页登录区域完成官方短信或扫码登录。

登录完成后通过官方 `/cowork/user_info` 验证身份，云端令牌写入 `TELEAGENT_DATA_DIR`，只返回独立随机的 `ta-...` 账号 Key。目录权限 0700，文件权限 0600，原子写入并同步磁盘。按用户归属和 UID 隔离，重新授权保留已存在的本地账号 Key。

已有账号可通过运维命令导入受保护的 JSON 文件，文件包含 `token`，可选 `device_id`、`install_id`，不会在日志中打印凭据：

```sh
TELEAGENT_DATA_DIR=/secure/teleagent TELEAGENT_LOGIN_OWNER=admin:<用户ID> \
  node src/import.mjs /secure/existing-token.json
```

## 推理与签到

`GET /v1/models`、`POST /v1/chat/completions` 要求同时提供 `Authorization: Bearer <账号Key>` 和 `X-Sub2API-Adapter-Key: <部署密钥>`。支持 `chat-pro`、`chat-flagship`、`chat-flash`、`chat-lite`、`chat-nano`，保留 messages、tools、stream 及原始请求字节。SSE 原样流转，上游错误状态原样保留；客户端断开或超时会取消上游请求。

默认每天 **Asia/Shanghai 00:10** 自动领取每日登录积分。服务器启动后补领当天漏签，已成功日期持久化至 `welfare.json`，失败有限重试。`TELEAGENT_WELFARE_ENABLED=0` 可关闭。令牌过期时须重新网页登录，不假设桌面客户端会自动刷新服务器凭据。

252 集群部署叠加 `deploy/docker-compose.teleagent.yml`，`.env` 设置 `SUB2API_TELEAGENT=1`，`deploy-252.sh` 会生成并持久化内部密钥。必须走完整集群或 TeamCity 发布流程。账号创建时选择已有 `codex` 分组，不自动创建或硬编码分组 ID。

## 验证与来源

```sh
npm test
bash ../../deploy/tests/teleagent-deploy-test.sh
```

Linux 包来自官网页面 `https://www.teleai.com.cn/product/teleagent` 的下载接口：`https://agent.teleai.com.cn/superCowork/sapi/api/download/redirect/linux-x64`，2026-10-15 返回 `TeleAgent-2.7.0-amd64.deb`。包内 Go 内核为静态链接 ELF，可在 CentOS 7 启动。实际云端签名以 Linux 内核反汇编和已有账号的服务器直连 HTTP 200 响应交叉验证。

签名第一层为 token 第三段作 HMAC-SHA256 Key，正文为 `/` 连接的版本常量、完整 token、install ID、秒级时间戳、nonce。第二层 Key 为第一层的十六进制文本，正文为换行连接的版本常量、HTTP 方法、完整上游路径、时间戳、nonce、应用版本号、最终正文 SHA256。对应实现和独立向量在 `src/upstream.mjs`、`test/upstream.test.mjs`。

真实短信或扫码的新账号授权必须由账号持有人完成。截图渲染及输入转发、已有会话导入与实际模型调用需要分别验收，不能用其中一项替代全部验证。
