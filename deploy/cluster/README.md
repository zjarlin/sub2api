# 252 Cluster Deployment

The stable client endpoint remains `192.168.31.252:18080`. Nginx owns that
port and always forwards to exactly one active `sub2api` application container.
PostgreSQL and Redis remain shared, so JWT secrets, scheduler state, and account
data continue to come from the existing `/opt/sub2api/.env`.

后端镜像构建默认通过清华 Alpine 镜像安装依赖，并保留 apk 官方签名校验。
可用 `--build-arg ALPINE_MIRROR=` 恢复基础镜像的软件源。版本号、提交号和构建日期
只影响最终 Go 编译层，后续发版复用依赖安装缓存。

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
