#!/usr/bin/env bash
#
# Edge Vision - 252 部署脚本
#
# 形态：不占用宿主端口，作为 sub2api 网关的内部上游，统一从 18080 的 /vision/ 访问。
#
# 用法：
#   ./scripts/deploy-252.sh                  # 同步代码，在 252 上构建并启动
#   ./scripts/deploy-252.sh --load FILE.tar  # 本地 docker save 产物，在 252 上 docker load 后启动
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
HOST="${EDGE_VISION_HOST:-192.168.31.252}"
REMOTE_DIR="${EDGE_VISION_REMOTE_DIR:-/opt/edge-vision}"
IMAGE_TAG="${EDGE_VISION_TAG:-1.0.0}"
# sub2api 网关对外入口与内部网络
PUBLIC_BASE="${EDGE_VISION_PUBLIC_BASE:-http://$HOST:18080}"
SUB2API_NETWORK="${SUB2API_NETWORK:-sub2api_sub2api-network}"

log() { printf '\033[1;34m[edge-vision]\033[0m %s\n' "$*"; }

if [[ "${1:-}" == "--load" ]]; then
  ARCHIVE="${2:?--load 需要指定 docker save 产物路径}"
  log "上传并加载镜像归档: $ARCHIVE"
  scp -q "$ARCHIVE" "$HOST:/tmp/edge-vision-image.tar"
  ssh "$HOST" "docker load -i /tmp/edge-vision-image.tar"
else
  log "同步代码到 $HOST:$REMOTE_DIR/edge-vision"
  rsync -az --delete \
    --exclude '.git' --exclude '__pycache__' --exclude '*.pyc' \
    "$PROJECT_DIR/" "$HOST:$REMOTE_DIR/edge-vision/"
  log "在 $HOST 上构建镜像 zjarlin/edge-vision:$IMAGE_TAG"
  ssh "$HOST" "cd $REMOTE_DIR/edge-vision && docker build -t zjarlin/edge-vision:$IMAGE_TAG -t zjarlin/edge-vision:latest -f docker/Dockerfile ."
fi

log "接入 sub2api 网络并启动容器（无宿主端口）"
ssh "$HOST" "docker network inspect $SUB2API_NETWORK >/dev/null 2>&1 || { echo '缺少 sub2api 网络，请先启动 sub2api 集群'; exit 1; }; \
  cd $REMOTE_DIR/edge-vision && \
  EDGE_VISION_IMAGE=zjarlin/edge-vision:$IMAGE_TAG \
  EDGE_VISION_MODELS_DIR=$REMOTE_DIR/edge-vision/models \
  SUB2API_NETWORK=$SUB2API_NETWORK \
  docker compose -f compose/docker-compose.yml up -d --force-recreate"

log "等待容器健康"
for _ in $(seq 1 30); do
  status="$(ssh "$HOST" "docker inspect edge-vision --format '{{.State.Health.Status}}' 2>/dev/null || echo missing")"
  if [[ "$status" == "healthy" ]]; then
    log "容器健康"
    break
  fi
  sleep 2
done

log "通过网关验证统一入口"
for _ in $(seq 1 15); do
  if curl -fsS --max-time 5 "$PUBLIC_BASE/vision/health" >/dev/null 2>&1; then
    log "统一入口可用: $PUBLIC_BASE/vision/health"
    curl -s "$PUBLIC_BASE/vision/health"
    echo
    exit 0
  fi
  sleep 2
done

log "网关路径不可用，请检查 sub2api-gateway 的 nginx.conf 是否包含 /vision/ 路由，以及 edge-vision 是否在同一网络"
ssh "$HOST" "docker logs --tail 50 edge-vision"
exit 1
