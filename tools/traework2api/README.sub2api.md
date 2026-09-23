# traework2api 作为 Sub2API 内置适配器

本目录是 [Sliverkiss/traework2api](https://github.com/Sliverkiss/traework2api) 的内置副本，
把 TRAE Work (SOLO CN) 的免费对话通道包装成 OpenAI 兼容接口，作为 Sub2API 的 sidecar 运行。

## 平台接入

Sub2API 已提供独立的 **TRAE Work（traework）** 平台类型。账号为 `apikey` 类型，
协议固定 `chat_completions`，并发固定 `1`。内置模式下账号表单**无需**填写地址和密钥：
后端从 `builtin_adapter.traework_url` 与 `builtin_adapter.traework_key` 注入。

## 启用内置适配器

1. 可直接启动空账号池，在 Sub2API 的添加/编辑账号表单选择 TRAE Work 后点击「登录并接入」。打开授权页完成登录，将地址栏的完整 `http://127.0.0.1:18080/authorize?...` 链接粘贴回表单，后台自动换票、保存并热加载。服务器不需要桌面，浏览器回环地址无法打开时直接复制即可。首次登录仍需手动粘贴一次回调链接，不宣称自动回调。

   也保留原有命令行登录方式：

   ```sh
   ./login.sh            # 或 docker run 挂载 auths/ 后执行
   ```

   成功后 `auths/trae-<uid>.json` 落盘，内含刷新凭证和上游验证的账号信息。

2. 在部署目录 `.env` 中设置：

   ```sh
   SUB2API_BUILTIN_ADAPTERS=1
   TRAEWORK_ADAPTER_KEY=<随机共享密钥>
   WORKBUDDY_ADAPTER_KEY=<另一随机共享密钥>
   ```

3. 用内置适配器 overlay 启动：

   ```sh
   docker compose --project-directory . \
     -f deploy/docker-compose.yml \
     -f deploy/docker-compose.builtin-adapters.yml \
     up -d --build
   ```

   252 集群部署脚本在 `SUB2API_BUILTIN_ADAPTERS=1` 时会自动叠加该文件。

4. 首次授权成功或已有账号池时，在「添加账号」选择 **TRAE Work**，填名称即可；模型默认 `glm-5.2`，可用 `/v1/models`
   拉取到的 32 个 `config_name` 之一。

## 运维

```sh
./signin.sh             # 批量签到（自动 refresh 过期 token）
./credit.sh             # 积分日报
```

凭证目录、`data/` 与 `config.json` 已在 `.gitignore` 中，不会进入仓库或镜像构建上下文。

默认使用 Docker 命名卷持久化，无需手工准备目录；仍可用 `TRAEWORK_AUTH_DIR` / `TRAEWORK_STATE_DIR` 覆盖为 UID 10001 可写的路径。多个 Sub2API TRAE 路由账号共享此登录池，凭证不保存到路由账号表。

共享登录会话模块位于 `tools/builtinlogin`，构建上下文改为 `tools`。直接构建使用 `docker build -f tools/traework2api/Dockerfile.sub2api tools`。登录回调处理只信任上游返回的账号身份，不信任链接中的 userInfo；回调凭证不进入审计日志。

## 边界

- 这是 TRAE SOLO 免费通道的反向代理，不是 TRAE 官方 API，稳定性取决于上游。
- 该上游只提供 Chat Completions；Responses / Anthropic 请求由 Sub2API 网关转换。
- 未验证原生 function calling 语义；工具调用行为以上游返回为准。
