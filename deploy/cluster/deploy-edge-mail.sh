#!/usr/bin/env bash
#
# 部署边缘计算邮件服务（edge-mail）到 252。
#
# 形态：不占用宿主端口，作为 sub2api 网关的内部上游，统一从 18080 的 /mail/ 访问。
# 服务无状态：不挂载模型或数据卷，账号信息全部随请求传入。
# 依赖 sub2api 集群已启动并创建了 sub2api_sub2api-network。
#
# 环境变量：
#   DEPLOY_DIR       部署根目录（默认 /opt/sub2api，TeamCity 会同步代码到这里）
#   EDGE_MAIL_IMAGE  镜像标签（必填，例如 zjarlin/edge-mail:<sha>）
#   SUB2API_NETWORK  sub2api 的 Compose 网络名（默认 sub2api_sub2api-network）
set -euo pipefail

DEPLOY_DIR="${DEPLOY_DIR:-/opt/sub2api}"
PROJECT_NAME="${PROJECT_NAME:-sub2api}"
EDGE_MAIL_DIR="${EDGE_MAIL_DIR:-$DEPLOY_DIR/edge-mail}"
IMAGE="${EDGE_MAIL_IMAGE:?EDGE_MAIL_IMAGE is required}"
NETWORK="${SUB2API_NETWORK:-${PROJECT_NAME}_sub2api-network}"

test -f "$EDGE_MAIL_DIR/docker/Dockerfile"
test -f "$EDGE_MAIL_DIR/compose/docker-compose.yml"
docker network inspect "$NETWORK" >/dev/null 2>&1 || {
  echo "缺少 sub2api 网络 $NETWORK，请先部署 sub2api 集群" >&2
  exit 1
}

echo "构建边缘邮件镜像 $IMAGE"
docker build \
  --build-arg "PYTHON_IMAGE=${EDGE_MAIL_PYTHON_IMAGE:-python:3.12-slim-bookworm}" \
  -t "$IMAGE" \
  -t zjarlin/edge-mail:latest \
  -f "$EDGE_MAIL_DIR/docker/Dockerfile" \
  "$EDGE_MAIL_DIR"

echo "启动边缘邮件容器（接入 $NETWORK，无宿主端口）"
EDGE_MAIL_IMAGE="$IMAGE" \
  MAIL_SEND_ENABLED="${MAIL_SEND_ENABLED:-true}" \
  MAIL_MAX_FETCH="${MAIL_MAX_FETCH:-50}" \
  MAIL_MAX_ATTACHMENT_BYTES="${MAIL_MAX_ATTACHMENT_BYTES:-26214400}" \
  MAIL_ALLOWED_PROVIDERS="${MAIL_ALLOWED_PROVIDERS:-}" \
  MAIL_ALLOW_EXTERNAL_RECIPIENTS="${MAIL_ALLOW_EXTERNAL_RECIPIENTS:-true}" \
  MAIL_DEFAULT_FROM_NAME="${MAIL_DEFAULT_FROM_NAME:-}" \
  SUB2API_NETWORK="$NETWORK" \
  docker compose -f "$EDGE_MAIL_DIR/compose/docker-compose.yml" up -d --force-recreate

echo "等待容器健康"
for _ in $(seq 1 30); do
  status="$(docker inspect edge-mail --format '{{.State.Health.Status}}' 2>/dev/null || echo missing)"
  if [ "$status" = "healthy" ]; then
    echo "edge-mail 健康"
    echo "DEPLOYED $IMAGE"
    exit 0
  fi
  sleep 2
done

echo "edge-mail 健康检查超时，最近日志：" >&2
docker logs --tail 50 edge-mail >&2 || true
exit 1
