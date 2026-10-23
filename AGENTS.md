# AGENTS.md — sub2api-chat-playground

> 本目录是 sub2api 源码工作区。**182 主机上同时运行着线上 sub2api 服务**，
> 你的模型后端也走这个入口，所以误操作会直接把自己打死。
> 入口主机**不写死**：一律取 `SUB2API_BASE_URL` / `APP_HOST` / `DEPLOY_HOST`
> 之类的环境变量或流水线参数；本机自查用 `127.0.0.1`。

## 主机与 IP 变量（强制）
- **主机地址一律从变量取，禁止硬编码**：脚本、流水线、compose、systemd、文档示例、
  测试、README 都不得写死内网 IP/主机名，统一从环境变量或流水线参数读取，例如
  `APP_HOST`、`DEPLOY_HOST`、`SUB2API_BASE_URL`、`SUB2API_PORT`、`TEAMCITY_AGENT_NAME`。
- **默认值只允许出现在一个地方**（流水线 `params` 或该服务的 `.env`）。其他位置必须引用
  变量；取不到时**报错退出**，不要静默回退到旧 IP。
- **现役主机已迁到 182**，`192.168.31.252` 已退役。可执行路径里出现 252 即为缺陷；
  只有历史事故记录/历史验证快照（`docs/incidents/*`、`docs/verification/*`）允许保留。
- 引用旧文档/旧流水线前先核实现役拓扑（`docker ps`、`/health`、TeamCity agent 列表、
  实际部署目录）；发现部署目录、agent 名、数据目录与文档不一致时先确认再改。
- 新脚本接入清单：唯一默认值来源 + 开头变量校验 + 只引用变量 +
  提交前 `git grep -nE '192\.168\.|10\.126\.' -- ':!docs/incidents' ':!docs/verification'` 必须为空。

## 硬性禁令
**禁止**用 `docker compose up/down/restart/recreate` 操作 `sub2api` 容器
（含 `-d / --no-deps / --force-recreate`，以及任何等价写法）。
历史上这些写法曾在 2026-10-08 造成两次生产中断（新容器抢 18080 → `exit 128`；
compose 先删健康容器 → 502）。182 是**单容器直发端口**的拓扑，直接 compose 重建
没有蓝绿保护，失败即全公司断模型。

## 正确做法
- 部署/重启/换镜像：走 TeamCity 流水线 `sub2api / Deploy 182 Gateway`，
  或本机 `bash deploy/gateway/deploy-gateway.sh`（两者都调用同一份脚本）。
  该脚本是「构建 → 候选端口预检 → 就地替换 → 健康复检 → 失败自动回滚」，
  变量：`APP_HOST`、`DEPLOY_DIR`（默认 `/root/DockerCompose/sub2api`）、
  `SUB2API_IMAGE`、`SUB2API_CONTAINER`、`SUB2API_PORT`、`CANARY_PORT`。
  流水线经 `/usr/local/sbin/sub2api-deploy-182` 受控入口执行，仓库内脚本必须已提交，
  否则干净 checkout 的构建会以 `missing deploy/gateway/deploy-gateway.sh` 失败。
- 新镜像已部署后，受保护订阅 `okm182` 下的旧本地固定会话仍钉在**已删除的旧镜像**上，
  重建容器后这些会话会失败（表现为 Thinking 无响应、无任何请求日志）。
  处理：换成订阅里的新会话/新会话 ID，旧会话不自动跟随。
- 部署标记写在 `$DEPLOY_DIR/DEPLOYED_IMAGE` 与 `$DEPLOY_DIR/DEPLOYED_RELEASE`；
  判断线上版本**以 `docker inspect` 的实际镜像为准**，标记文件可能过期。
- 数据/配置统一收敛在 `/root/DockerCompose/<服务名>/`，compose 文件与数据卷同目录。

## 只读排查随意
`docker ps/logs/inspect`、`curl 127.0.0.1:18080/health`、`git`、读日志，均不受限。
**不要**因为容器 `unhealthy` 就顺手重启：先查健康检查本身为何失败。

## 服务保护（最高优先级）
不得停止/重启/删除 `sub2api` 容器；不得让构建、迁移、清理、Docker 网络维护波及它。
任何涉及共享资源（网络、iptables、磁盘、端口、内存）的操作完成后，
必须确认 `curl -fsS 127.0.0.1:18080/health` 为 200 且容器 `running`，再做别的事。

## 并发会话
本工作区常有多个 Codex/TeamCity 会话同时改动：**禁止** `git add -A` / `git stash` /
`git reset --hard` / `git clean`；提交只用 `git commit --only <本任务路径>`。
