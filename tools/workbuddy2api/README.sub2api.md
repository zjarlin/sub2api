# WorkBuddy 内置平台

来源：[Sliverkiss/workbuddy2api](https://github.com/Sliverkiss/workbuddy2api)，固定版本
`1c9aef9ab378217316ee2ddff7eace78381b46c0`。保留上游 MIT LICENSE。

Sub2API 把它作为 `workbuddy` 平台的内部服务一起部署，无需另找中转服务或在账号表单填写 Base URL / API Key。适配器是独立进程，和主服务使用同一套 Compose 编排，不是编进主服务二进制。

## 部署

启用 `deploy/docker-compose.builtin-adapters.yml`，并为 `TRAEWORK_ADAPTER_KEY`、`WORKBUDDY_ADAPTER_KEY` 设置不同的随机共享密钥。豆包仍需按桌面适配器文档准备其已有登录会话。TRAE / WorkBuddy 凭证与状态默认保存在 Docker 命名卷中，首次启动可以为空，不需要先运行登录脚本。

在仓库根目录执行（基础 Compose 还需要数据库等已有配置）：

```sh
docker compose --project-directory . \
  -f deploy/docker-compose.yml \
  -f deploy/docker-compose.builtin-adapters.yml up -d --build
```

252 集群脚本使用 `SUB2API_BUILTIN_ADAPTERS=1` 启用上述编排。后台读取 `BUILTIN_ADAPTER_WORKBUDDY_URL` 和 `BUILTIN_ADAPTER_WORKBUDDY_KEY`，默认内部地址 `http://sub2api-workbuddy:7863`；适配器端口不发布到公网。自定义 bind mount 时目录必须允许 UID 10001 读写，或直接使用默认命名卷。

## 登录与调用

1. 添加或编辑账号，选择 **WorkBuddy**，点击「登录并接入」。
2. 点击「打开授权页」，在用户浏览器完成国内版登录。后台轮询上游设备授权，无需回环端口、桌面客户端或手动复制上下文。
3. 页面显示「已接入」后，凭证已按 0600 权限写入 `auths/workbuddy-<uid>.json` 并热加载。保存 Sub2API 账号，自动同步模型目录。
4. 调用 Sub2API 的常规 OpenAI 兼容接口。默认模型 `glm-5.2`，实际支持列表以同步结果为准。工具请求由适配器转成上游协议，工具执行仍由 API 调用方负责。

一个部署内的多个 `workbuddy` 路由账号共享同一个上游登录账号池；重新登录更新这个池。路由账号自身使用 `apikey` 类型，它保存的是内部服务连接信息。OAuth 凭证留在适配器卷中，刷新由适配器维护。内置配置只启用凭证保活，不自动执行上游积分活动。

登录会话十分钟过期，按管理员身份隔离，多副本主服务共用适配器会话。回调/轮询请求体不进入审计日志；浏览器只拿授权地址和脱敏账号信息。适配器重启后未完成的授权需要重试，已落盘凭证会恢复。

`/livez` 是编排存活检查（空池也返回 200），`/healthz` 保留上游可调用状态检查（空池返回 503）。构建上下文是 `tools`，共享登录实现位于 `tools/builtinlogin`。直接构建可用 `docker build -f tools/workbuddy2api/Dockerfile.sub2api tools`。

## 验证边界

覆盖平台创建/路由、共享会话隔离和到期、取消、回调去重、凭证原子保存、热加载、错误脱敏与浏览器表单交互。真实上游已验证授权地址生成与未登录时的 `11217` 等待状态；成功授权及模型生成仍需用真实账号完成验收。
