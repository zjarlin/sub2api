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
