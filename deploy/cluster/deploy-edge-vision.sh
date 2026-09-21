#!/usr/bin/env bash
#
# 部署边缘计算视觉服务（edge-vision）到 252。
#
# 形态：不占用宿主端口，作为 sub2api 网关的内部上游，统一从 18080 的 /vision/ 访问。
# 依赖 sub2api 集群已启动并创建了 sub2api_sub2api-network。
#
# 环境变量：
#   DEPLOY_DIR       部署根目录（默认 /opt/sub2api，TeamCity 会同步代码到这里）
#   EDGE_VISION_IMAGE 镜像标签（必填，例如 zjarlin/edge-vision:<sha>）
#   SUB2API_NETWORK  sub2api 的 Compose 网络名（默认 sub2api_sub2api-network）
set -euo pipefail

DEPLOY_DIR="${DEPLOY_DIR:-/opt/sub2api}"
PROJECT_NAME="${PROJECT_NAME:-sub2api}"
EDGE_VISION_DIR="${EDGE_VISION_DIR:-$DEPLOY_DIR/edge-vision}"
IMAGE="${EDGE_VISION_IMAGE:?EDGE_VISION_IMAGE is required}"
NETWORK="${SUB2API_NETWORK:-${PROJECT_NAME}_sub2api-network}"

test -f "$EDGE_VISION_DIR/docker/Dockerfile"
test -f "$EDGE_VISION_DIR/compose/docker-compose.yml"
docker network inspect "$NETWORK" >/dev/null 2>&1 || {
  echo "缺少 sub2api 网络 $NETWORK，请先部署 sub2api 集群" >&2
  exit 1
}

echo "构建边缘视觉镜像 $IMAGE"
docker build \
  -t "$IMAGE" \
  -t zjarlin/edge-vision:latest \
  -f "$EDGE_VISION_DIR/docker/Dockerfile" \
  "$EDGE_VISION_DIR"

echo "启动边缘视觉容器（接入 $NETWORK，无宿主端口）"
EDGE_VISION_IMAGE="$IMAGE" \
  EDGE_VISION_MODELS_DIR="$EDGE_VISION_DIR/models" \
  SUB2API_NETWORK="$NETWORK" \
  docker compose -f "$EDGE_VISION_DIR/compose/docker-compose.yml" up -d --force-recreate

echo "等待容器健康"
for _ in $(seq 1 30); do
  status="$(docker inspect edge-vision --format '{{.State.Health.Status}}' 2>/dev/null || echo missing)"
  if [ "$status" = "healthy" ]; then
    echo "edge-vision 健康"
    echo "DEPLOYED $IMAGE"
    exit 0
  fi
  sleep 2
done

echo "edge-vision 健康检查超时，最近日志：" >&2
docker logs --tail 50 edge-vision >&2 || true
exit 1
