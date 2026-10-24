# Mac mini 上的 sub2api 部署

本目录负责把**本仓库源码构建的镜像**部署到 Mac mini 上的 `sub2api` 实例。
它与 182 主机上的受保护网关（`127.0.0.1:18080`）完全独立：两条流水线、两个数据目录、
两个数据库，互不触发，也不共享容器。

## 拓扑

| 项 | 值 |
| --- | --- |
| 打包流水线 | `.github/workflows/macmini-deploy.yml`（GitHub Actions） |
| 执行机 | Mac mini 自托管 runner，标签 `self-hosted, macOS, ARM64, macmini-sub2api` |
| compose 目录 | `DEPLOY_DIR`（默认 `~/DockerCompose/dockge/stacks/sub2api`） |
| 数据目录 | `DATA_DIR`（默认 `~/DockerCompose/sub2api/data`） |
| 数据库 | 共享容器 `pgvector` 内的 `sub2api` 库（不新建实例） |
| 缓存 | 共享容器 `redis`（不新建实例） |
| 入口 | 单容器直发宿主 `18080`，健康检查 `http://127.0.0.1:18080/health` |

脚本只替换 `sub2api` 这一个容器，不重建也不重启 `pgvector` 和 `redis`。
镜像构建在 Mac mini 本地完成（`linux/arm64`），不推送 registry。

## 一次性准备：自托管 runner

1. 下载 runner 并解压，例如 `~/actions-runner-sub2api`。
2. 用仓库注册 token 配置（token 一次性，不要写入仓库）：
   `./config.sh --unattended --url https://github.com/zjarlin/sub2api --token <token> --name macmini-sub2api --labels macmini-sub2api --work _work --replace`
3. 常驻：`./svc.sh install && ./svc.sh start`，`./svc.sh status` 显示 Started。
4. 校验：`gh api repos/zjarlin/sub2api/actions/runners` 中该 runner 为 `online`，
   labels 含 `macmini-sub2api`。

runner 需要能直接跑 `docker`（`/usr/local/bin` 或 `/opt/homebrew/bin` 在 PATH 里）。

## 部署流程（`deploy-macmini.sh`）

1. 校验 `DEPLOY_DIR/docker-compose.yml`、`DATA_DIR`、现役容器与数据库容器存在；
   `DATA_DIR` 必须是现役 `/app/data` 的 bind 源。
2. `SUB2API_BUILD=1` 时用 `docker buildx build --platform linux/arm64 --load` 构建
   `zjarlin/sub2api:<短 SHA>`；`EXPECTED_GIT_SHA` 与仓库 HEAD 不一致时直接拒绝。
3. 切流前 `pg_dump -Fc` 备份数据库，并用 `pg_restore -l` 校验可读（默认 `BACKUP_DATABASE=1`）。
4. 在 `127.0.0.1:<CANARY_PORT>` 起候选容器，沿用现役容器的网络、环境变量与 bind 挂载，
   等 `/health` 200；候选不健康就退出，**不切流**。
5. 写 `DEPLOY_DIR/docker-compose.deploy.yml`（只固定镜像），
   执行 `docker compose up -d --no-deps sub2api` 就地切流。
6. 切流后复查 `/health` 与 `healthy`；失败按上一镜像 ID 回滚。
7. 写 `DEPLOY_DIR/DEPLOYED_IMAGE` 与 `DEPLOYED_RELEASE`。线上版本以 `docker inspect` 为准。

脚本拒绝复制 named volume：本机约定所有持久化数据都是 bind mount。

> 不要用 `docker compose up/restart` 直接操作 `sub2api`，也不要在它所属的 compose 项目里
> `down`。切流必须走本脚本或 workflow，才有备份、候选预检和回滚。

## 变量

| 变量 | 默认 | 说明 |
| --- | --- | --- |
| `DEPLOY_DIR` | `~/DockerCompose/dockge/stacks/sub2api` | compose 与部署标记 |
| `DATA_DIR` | `~/DockerCompose/sub2api/data` | 持久化数据目录 |
| `BACKUP_DIR` | `$DEPLOY_DIR/backups` | 数据库备份 |
| `SUB2API_BUILD` | `1` | 是否在 Mac mini 本地构建 |
| `SUB2API_GIT_SHA` | `git rev-parse HEAD` | 镜像 tag 与构建版本 |
| `EXPECTED_GIT_SHA` | 空 | 设置后要求与 HEAD 一致 |
| `SUB2API_IMAGE` | `zjarlin/sub2api:<短 SHA>` | 可复用已有镜像 |
| `SUB2API_PORT` / `CANARY_PORT` | `18080` / 自动 | 宿主端口 / 候选 loopback 端口 |
| `LIVE_WAIT_SECONDS` | `180` | 切流后健康等待 |
| `DATABASE_CONTAINER` / `DATABASE_NAME` | `pgvector` / `sub2api` | 备份与迁移目标 |

仓库变量 `SUB2API_MACMINI_DEPLOY_DIR`、`SUB2API_MACMINI_DATA_DIR` 可选；不配置时用脚本默认值，
默认值只在这两个地方维护。

## 隔离预演（不碰现役库和现役数据）

先把现役库恢复成独立的预演库，再让候选只连预演库、只挂独立数据目录：

```bash
CANARY_ONLY=1 DATA_DIR_MUST_MATCH=0 \
CANARY_DATABASE_NAME=<预演库> \
DATA_DIR=<独立数据目录> BACKUP_DATABASE=0 \
SUB2API_BUILD=0 SUB2API_IMAGE=<已有镜像> \
bash deploy/macmini/deploy-macmini.sh
```

`DATA_DIR_MUST_MATCH=0` 必须同时给 `CANARY_DATABASE_NAME` 和 `CANARY_ONLY=1`，否则脚本拒绝执行。
预演容器保留在 `sub2api-canary-<pid>`，验证后 `docker rm -f` 清理。

## 迁移与回滚边界

- 新镜像启动时会执行 `backend/migrations` 里尚未应用的迁移；候选启动就等于开始迁移，
  所以备份必须先于候选。
- 迁移**不可逆**：把容器回滚到旧镜像不会回滚已升级的 schema/数据。
  回滚前先评估旧镜像能否运行在新 schema 上；必要时从备份恢复数据库。
- 已知的破坏性迁移只有按语义清理“全无限制”的 `user_platform_quotas` 行，
  不携带任何配额配置。

## 触发规则

- 只有推送 `main` 且命中 workflow 的 `paths` 才会部署；`pull_request` 不触发。
- TeamCity 的 `Deploy182Gateway` 与 `DeployTianjinMedia` 已把
  `deploy/macmini/**`、`deploy/tests/macmini-deploy-test.sh`、`.github/workflows/macmini-deploy.yml`
  排除出 `triggerRules`，所以改本目录不会重启 182 网关或天津媒体服务。

## 本地验证

```bash
bash -n deploy/macmini/deploy-macmini.sh
bash deploy/tests/macmini-deploy-test.sh   # 用假的 docker/curl，不连接任何真实主机
```
