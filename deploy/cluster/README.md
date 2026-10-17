# 252 Cluster Deployment

The stable client endpoint remains `192.168.31.252:18080`. Nginx owns that
port and always forwards to exactly one active `sub2api` application container.
PostgreSQL and Redis remain shared, so JWT secrets, scheduler state, and account
data continue to come from the existing `/opt/sub2api/.env`.

后端镜像构建默认通过清华 Alpine 镜像安装依赖，并保留 apk 官方签名校验。
可用 `--build-arg ALPINE_MIRROR=` 恢复基础镜像的软件源。版本号、提交号和构建日期
只影响最终 Go 编译层，后续发版复用依赖安装缓存。

## Edge 服务按需更新

`edge-vision`、`edge-media`、`edge-mail` 和 `edge-laya` 各自已有
`compose/docker-compose.yml`，复用 Sub2API 内部网络，不发布宿主端口。

TeamCity 只传递各服务的 `EDGE_*_IMAGE_REPOSITORY`。部署脚本对该服务的
Dockerfile、应用目录、依赖清单、Docker ignore 文件和实际传入的构建参数计算摘要，
使用 `<repository>:src-<sha256>` 标签；只有视觉服务还将随镜像打包的 `models/`
计入摘要。仅挂载的媒体数据、Laya 权重和 Compose 运行配置不改变镜像版本。

- 镜像已存在：跳过 `docker build`，不依赖整个仓库的提交号。
- 镜像与运行配置未变：Compose 保留原容器；源码同步造成的时间戳、属主变化不触发构建。
- 只有运行配置变化：复用镜像，由 Compose 更新对应服务。
- 构建内容变化或镜像被清理：仅构建对应服务，构建失败立即停止部署。

首次切换到内容标签时会更新一次容器，之后无变化的发布保留容器。
可显式传 `EDGE_*_IMAGE` 选择不可变版本；已有版本直接复用，不能用同一标签承载不同源码。
需要刷新浮动基础镜像时，先在专门的镜像发布流程拉取基础镜像，再用
`EDGE_FORCE_BUILD=1` 重跑该服务部署脚本。健康检查仍在每次部署执行。

验证：`bash deploy/tests/edge-deploy-test.sh` 使用隔离的 Docker 命令替身，
覆盖无变化复用、各服务独立更新、构建参数、配置和权重边界，以及构建失败传播。

## Release Flow

The normal release path is a single-host blue-green handoff:

1. Build the immutable image and keep the current `18080` listener running.
2. Start the new image as a one-off `sub2api-canary` container on loopback
   `127.0.0.1:18090`, using the same Compose service definition, data volume,
   database, Redis, and network.
3. Probe the candidate `/ready`, `/health`, and frontend endpoint.
4. Point only the stable Nginx gateway at `sub2api-canary`, reload Nginx, and
   probe `18080` again. The gateway process is not restarted.
5. Start the replacement primary while the candidate keeps serving `18080`.
6. Probe the replacement primary directly through the gateway container, move
   `18080` back to `sub2api`, and remove the candidate.

If the candidate fails before the switch, `18080` remains on the current
primary and the candidate is removed. If the switch probe fails, the gateway is
restored to the previous primary. If primary promotion fails after cutover,
the verified candidate remains in place and the deployment reports failure.

The first migration from a legacy direct `sub2api:18080` listener can have a
short handoff window, because that old container owns the port itself. Every
later release has no application-container cutover gap. A host, Docker daemon,
network interface, or Cloudflare Tunnel failure still requires host-level
redundancy; this mechanism only protects the application release path.

## 内置适配器：单文件 + 环境变量开关

所有可选适配器统一写在 `deploy/docker-compose.adapters.yml` 一个文件里，
每个服务带自己的 compose `profile`；启不启用只由目标机 `.env` 的开关决定：

```env
SUB2API_WORKBUDDY=1     # 只开 WorkBuddy
SUB2API_CURSOR=0        # 关
```

部署时 `deploy/cluster/adapters.env.sh` 把开关解析成：

- `COMPOSE_PROFILES`（决定启动哪些适配器容器）
- `BUILTIN_ADAPTER_<P>_URL`（注入主服务；未启用平台为空串 → 管理页判为未启用）
- `<P>_ADAPTER_KEY`（首次部署自动生成并回写 `.env`）

`SUB2API_BUILTIN_ADAPTERS=1` 作为兼容开关保留，等价于同时启用
`desktop / traework / workbuddy / vibex / zcode`。

单一数据源在 `deploy/cluster/adapters.env.sh` 的 `ADAPTER_REGISTRY`：新增平台
只需加一行，不需要再改 compose 叠加清单或流水线。

## 通用主机部署脚本

`deploy/tianjin/deploy-adapter-host.sh` 可在任意主机部署同一套栈，换机器只换
目标：

```bash
TARGET_SSH_HOST=tianjin-media \
TARGET_DEPLOY_DIR=/opt/reeve/stacks/sub2api \
SUB2API_IMAGE=zjarlin/sub2api:<sha> \
  deploy/tianjin/deploy-adapter-host.sh
```

执行体 `deploy/tianjin/deploy-adapter-host-remote.sh` 只做四件事：读开关、
补密钥、切镜像、按 profile 重建 `sub2api` 与已启用适配器。它**不碰**
postgres/redis（不同机器的数据库容器名可能不同，按 compose 拉起会新建空库），
也不在目标机重建主镜像（镜像由流水线构建后传入）。

TeamCity 对应 `Deploy Tianjin Sub2API`：构建镜像 → 经 SSH 传到目标机 →
同步 `deploy/` 与 `tools/` → 在目标机执行上述执行体 → 健康检查。当前该流水线
只通过 `SUB2API_WORKBUDDY=1` 启用 WorkBuddy。
