# Mac mini 上的 sub2api 部署

本目录负责把**本仓库源码构建的镜像**部署到 Mac mini 上的 `sub2api` 实例。
它与 182 主机上的受保护网关（`127.0.0.1:18080`）完全独立：两条流水线、两个数据目录、
两个数据库，互不触发，也不共享容器。

## 拓扑

| 项 | 值 |
| --- | --- |
| 流水线 A（首选） | `.github/workflows/macmini-deploy.yml`（GitHub Actions，需仓库启用 Actions） |
| 流水线 B（当前生效） | `macmini-watch.sh` + launchd，Mac mini 本机轮询 `origin/main` |
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

## 触发方式：Actions 与轮询守护

两条通道**调用同一个 `deploy-macmini.sh`**，不维护第二套部署实现。

### 现状：本 fork 的 GitHub Actions 被仓库级禁用

2026-10-24 实测：仓库 `zjarlin/sub2api` 是 `Wei-Shaw/sub2api` 的 fork，
`gh workflow run` 返回 `HTTP 422: Actions has been disabled for this repository`，
而 `GET /repos/zjarlin/sub2api/actions/permissions` 仍返回 `enabled: true, allowed_actions: all`，
`PUT` 重新启用也无效；同账号的非 fork 仓库可正常派发，因此不是账号级限制。
`.yml` 已注册为 `active`，但推送不会产生 run。

结论：**不要在故障期间依赖 Actions 自动触发**。恢复步骤（需要人在 GitHub 网页操作）：

1. `https://github.com/zjarlin/sub2api/settings/actions` 确认 Actions 为启用状态；
   若看到 fork 相关提示，按页面指引 Enable 一次。
2. 用 `gh workflow run macmini-deploy.yml --ref main` 验证能派发成功（应返回 204/无报错）。
3. 之后推送 `main` 命中 `paths` 即自动部署；runner 已在 `online`。

### 当前生效的通道：`macmini-watch.sh`

`macmini-watch.sh` 每 `WATCH_INTERVAL` 秒比对 `origin/main` 与「已部署提交」：

- 远端没变 → 什么都不做。
- 远端前进 → `git fetch` 后 `checkout -B main FETCH_HEAD`，再调 `deploy-macmini.sh`
  （带 `SUB2API_GIT_SHA` 与 `EXPECTED_GIT_SHA`，构建/备份/候选预检/切流/回滚全部复用）。
- `WATCH_PATHS`（默认与 workflow 的 `push.paths` 一致）决定哪些**文件内容**值得重建镜像：
  只改文档、`deploy/**` 脚本或测试时只记录提交、**不重启线上容器**；
  只调整可执行位而内容不变时也只记录提交。
  `WATCH_PATHS=`（空）表示检查整棵工作树的内容变化，不把每个新提交都当重建依据。
- 构建输入包括 `Dockerfile`、`Dockerfile.goreleaser`、`deploy/docker-entrypoint.sh`，
  内容变化会自动重建；部署测试与轮询规则需同时更新。
- 工作副本有本地改动 → 只记录并跳过，**不**强改工作区。
- 部署失败 → 不写状态文件，下个周期重试；日志留在 launchd 的 `stdout/stderr` 文件里。
- 状态文件 `WATCH_STATE`（默认 `$REPO_DIR/.macmini-watch.deployed`）必须被 `.gitignore` 覆盖，
  否则它自己会把工作区弄脏。

launchd 安装（在 Mac mini 上，路径都从变量取）：

```bash
REPO_DIR="$HOME/sub2api-deploy-src"                       # 源码工作副本
PLIST="$HOME/Library/LaunchAgents/site.addzero.sub2api-watch.plist"
mkdir -p "$HOME/Library/Logs"
cat > "$PLIST" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>site.addzero.sub2api-watch</string>
  <key>ProgramArguments</key><array>
    <string>/bin/bash</string>
    <string>$REPO_DIR/deploy/macmini/macmini-watch.sh</string>
  </array>
  <key>EnvironmentVariables</key><dict>
    <key>REPO_DIR</key><string>$REPO_DIR</string>
    <key>DEPLOY_DIR</key><string>$HOME/DockerCompose/dockge/stacks/sub2api</string>
    <key>DATA_DIR</key><string>$HOME/DockerCompose/sub2api/data</string>
    <key>WATCH_INTERVAL</key><string>120</string>
    <!-- 可选：留空字符串 = 检查整棵工作树；删除此项 = 用脚本默认（与 workflow paths 一致） -->
    <key>PATH</key><string>/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin</string>
  </dict>
  <key>WorkingDirectory</key><string>$REPO_DIR</string>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>$HOME/Library/Logs/sub2api-watch.log</string>
  <key>StandardErrorPath</key><string>$HOME/Library/Logs/sub2api-watch.err.log</string>
</dict></plist>
PLIST
launchctl unload "$PLIST" 2>/dev/null || true
launchctl load -w "$PLIST"
launchctl list | grep sub2api-watch
```

手动验证（只跑一个周期）：`WATCH_ONCE=1 REPO_DIR="$REPO_DIR" bash "$REPO_DIR/deploy/macmini/macmini-watch.sh"`。

## 部署流程（`deploy-macmini.sh`）

1. 校验 `DEPLOY_DIR/docker-compose.yml`、`DATA_DIR`、现役容器与数据库容器存在；
   `DATA_DIR` 必须是现役 `/app/data` 的 bind 源。
2. `SUB2API_BUILD=1` 时用 `docker buildx build --platform linux/arm64 --load` 构建
   `zjarlin/sub2api:<短 SHA>`；`EXPECTED_GIT_SHA` 与仓库 HEAD 不一致时直接拒绝。
3. 切流前 `pg_dump -Fc` 备份数据库，并用 `pg_restore -l` 校验可读（默认 `BACKUP_DATABASE=1`）。
4. 记录现役库的 `accounts` 总数和 `account_groups` 关联总数。
5. 在 `127.0.0.1:<CANARY_PORT>` 起候选容器，沿用现役容器的网络、环境变量与 bind 挂载，
   等 `/health` 200；候选不健康就退出，**不切流**。
6. 候选已执行迁移后核查账号数、账号分组关联数；任一数量变小或读取失败都不切流。
7. 写 `DEPLOY_DIR/docker-compose.deploy.yml`（只固定镜像），
   执行 `docker compose up -d --no-deps sub2api` 就地切流。
8. 切流后复查 `/health` 与 `healthy`；失败按上一镜像 ID 回滚。
9. 写 `DEPLOY_DIR/DEPLOYED_IMAGE` 与 `DEPLOYED_RELEASE`。线上版本以 `docker inspect` 为准。

账号数基线按现役库读取；可另设 `EXPECTED_ACCOUNT_COUNT` 和 `EXPECTED_ACCOUNT_GROUP_COUNT`
固定预期的数量。数字不一致即拒绝部署。隔离预演只核对预演库本身，不读取现役库。

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
| `DATABASE_CONTAINER` / `DATABASE_NAME` | `pgvector` / `sub2api` | 备份、迁移与账号核验目标 |
| `EXPECTED_ACCOUNT_COUNT` | 切流前现役库实测值 | 可选的账号总数预期 |
| `EXPECTED_ACCOUNT_GROUP_COUNT` | 切流前现役库实测值 | 可选的账号分组关联数预期 |

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

- Actions 通道（恢复后）：只有推送 `main` 且命中 workflow 的 `paths` 才会部署；`pull_request` 不触发。
- 轮询通道（当前）：`origin/main` 前进后，只对 `WATCH_PATHS` 内的**文件内容**变化部署；
  只改权限位、文档或测试时前进部署标记，保持与线上源码提交一致。
  两条通道都部署完成后，会因「已部署提交相同」而互相跳过。
- TeamCity 的 `Deploy182Gateway` 与 `DeployTianjinMedia` 已把
  `deploy/macmini/**`、`deploy/tests/macmini-deploy-test.sh`、`.github/workflows/macmini-deploy.yml`
  排除出 `triggerRules`，所以改本目录不会重启 182 网关或天津媒体服务。

## 本地验证

```bash
bash -n deploy/macmini/deploy-macmini.sh
bash -n deploy/macmini/macmini-watch.sh
bash deploy/tests/macmini-deploy-test.sh   # 用假的 docker/curl，不连接任何真实主机
bash deploy/tests/macmini-watch-test.sh    # 用临时 git 仓库和假部署脚本，不调用 docker
```
