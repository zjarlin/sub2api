#!/usr/bin/env bash
# =============================================================================
# 通用「远程主机部署 Sub2API + 内置适配器」脚本（单一入口，换机只换参数）。
# =============================================================================
# 在任意可通过 SSH 到达的主机上，用同一份代码与同一份
# deploy/docker-compose.adapters.yml 部署 Sub2API。
# 具体启用哪些适配器，只由「目标机 .env 的开关」决定：
#   SUB2API_WORKBUDDY=1        -> 启用 WorkBuddy 适配器
#   SUB2API_BUILTIN_ADAPTERS=1 -> （兼容旧开关）启用 desktop/traework/workbuddy/vibex/zcode
# 换机器 = 换 TARGET_SSH_HOST 与 TARGET_DEPLOY_DIR，脚本与 compose 不用改。
#
# 用法（通常在 252 的 TeamCity agent 上执行）：
#   TARGET_SSH_HOST=tianjin-media \
#   TARGET_DEPLOY_DIR=/opt/reeve/stacks/sub2api \
#   SUB2API_IMAGE=zjarlin/sub2api:<sha> \
#   SKIP_BUILD=0 \
#     deploy/tianjin/deploy-adapter-host.sh
#
# 环境变量：
#   TARGET_SSH_HOST    SSH 别名/主机（默认 tianjin-media）
#   TARGET_DEPLOY_DIR  目标机部署目录（默认 /opt/reeve/stacks/sub2api）
#   SUB2API_IMAGE      要部署的镜像 tag（必填；本地已存在的 tag，或目标机可拉取）
#   STACK_COMPOSE      目标机上的 compose 文件名（默认 docker-compose.reeve.yml）
#   SKIP_BUILD=1       不构建适配器镜像（默认 0：按需构建）
#   HEALTH_TIMEOUT     等待健康检查秒数（默认 180）
# =============================================================================
set -euo pipefail

TARGET_SSH_HOST="${TARGET_SSH_HOST:-tianjin-media}"
TARGET_DEPLOY_DIR="${TARGET_DEPLOY_DIR:-/opt/reeve/stacks/sub2api}"
SUB2API_IMAGE="${SUB2API_IMAGE:?SUB2API_IMAGE is required}"
STACK_COMPOSE="${STACK_COMPOSE:-docker-compose.reeve.yml}"
SKIP_BUILD="${SKIP_BUILD:-0}"
HEALTH_TIMEOUT="${HEALTH_TIMEOUT:-180}"
ADAPTER_COMPOSE="deploy/docker-compose.adapters.yml"

# 源码根目录：默认为本脚本上两级（deploy/tianjin -> 仓库根）。
REPO_DIR="${REPO_DIR:-$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)}"

# 执行体单独存放：本机直接运行，远程经 SSH 传入，逻辑只维护一份。
RUNNER="$REPO_DIR/deploy/tianjin/deploy-adapter-host-remote.sh"
[ -f "$RUNNER" ] || { echo "缺少 $RUNNER" >&2; exit 1; }

if [ -z "${TARGET_SSH_HOST:-}" ] || [ "$TARGET_SSH_HOST" = "local" ] || [ "$TARGET_SSH_HOST" = "localhost" ]; then
  echo "==> 本机执行"
  TARGET_DEPLOY_DIR="$TARGET_DEPLOY_DIR" STACK_COMPOSE="$STACK_COMPOSE" \
    SUB2API_IMAGE="$SUB2API_IMAGE" SKIP_BUILD="$SKIP_BUILD" HEALTH_TIMEOUT="$HEALTH_TIMEOUT" \
    bash "$RUNNER"
else
  echo "==> 经 SSH 在 $TARGET_SSH_HOST 执行"
  ssh -o BatchMode=yes -o ConnectTimeout=20 "$TARGET_SSH_HOST" \
    "TARGET_DEPLOY_DIR='$TARGET_DEPLOY_DIR' STACK_COMPOSE='$STACK_COMPOSE' \
     SUB2API_IMAGE='$SUB2API_IMAGE' SKIP_BUILD='$SKIP_BUILD' HEALTH_TIMEOUT='$HEALTH_TIMEOUT' \
     bash -s" < "$RUNNER"
fi
