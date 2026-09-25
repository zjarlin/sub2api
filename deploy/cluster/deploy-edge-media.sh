#!/usr/bin/env bash
#
# 部署边缘媒体服务（edge-media）到 252。
#
# 形态：不占用宿主端口，统一从 18080 的 /media/ 访问。
# 服务本身不包含 GPT-SoVITS 或视频生成模型；通过环境变量接入已准备的模型服务。
set -euo pipefail

DEPLOY_DIR="${DEPLOY_DIR:-/opt/sub2api}"
PROJECT_NAME="${PROJECT_NAME:-sub2api}"
EDGE_MEDIA_DIR="${EDGE_MEDIA_DIR:-$DEPLOY_DIR/edge-media}"
IMAGE="${EDGE_MEDIA_IMAGE:?EDGE_MEDIA_IMAGE is required}"
NETWORK="${SUB2API_NETWORK:-${PROJECT_NAME}_sub2api-network}"

test -f "$EDGE_MEDIA_DIR/docker/Dockerfile"
test -f "$EDGE_MEDIA_DIR/compose/docker-compose.yml"
docker network inspect "$NETWORK" >/dev/null 2>&1 || {
  echo "缺少 sub2api 网络 $NETWORK，请先部署 sub2api 集群" >&2
  exit 1
}

echo "构建边缘媒体镜像 $IMAGE"
docker build \
  -t "$IMAGE" \
  -t zjarlin/edge-media:latest \
  -f "$EDGE_MEDIA_DIR/docker/Dockerfile" \
  "$EDGE_MEDIA_DIR"

echo "启动边缘媒体容器（接入 $NETWORK，无宿主端口）"
EDGE_MEDIA_IMAGE="$IMAGE" \
  EDGE_MEDIA_MODELS_DIR="${EDGE_MEDIA_MODELS_DIR:-$EDGE_MEDIA_DIR/models}" \
  EDGE_MEDIA_DATA_DIR="${EDGE_MEDIA_DATA_DIR:-$DEPLOY_DIR/data/edge-media}" \
  MEDIA_TTS_ENABLED="${MEDIA_TTS_ENABLED:-false}" \
  MEDIA_TTS_UPSTREAM_URL="${MEDIA_TTS_UPSTREAM_URL:-http://gpt-sovits:9880}" \
  MEDIA_TTS_REFER_WAV="${MEDIA_TTS_REFER_WAV:-}" \
  MEDIA_TTS_PROMPT_TEXT="${MEDIA_TTS_PROMPT_TEXT:-}" \
  MEDIA_DUBBING_ENABLED="${MEDIA_DUBBING_ENABLED:-true}" \
  MEDIA_DUBBING_COMMAND="${MEDIA_DUBBING_COMMAND:-}" \
  MEDIA_VIDEO_UPSTREAM_URL="${MEDIA_VIDEO_UPSTREAM_URL:-}" \
  MEDIA_VIDEO_GENERATION_ENABLED="${MEDIA_VIDEO_GENERATION_ENABLED:-false}" \
  MEDIA_VIDEO_GENERATION_UPSTREAM_URL="${MEDIA_VIDEO_GENERATION_UPSTREAM_URL:-}" \
  MEDIA_TRANSCODE_COMMAND="${MEDIA_TRANSCODE_COMMAND:-}" \
  SUB2API_NETWORK="$NETWORK" \
  docker compose -f "$EDGE_MEDIA_DIR/compose/docker-compose.yml" up -d --force-recreate

echo "等待容器健康"
for _ in $(seq 1 30); do
  status="$(docker inspect edge-media --format '{{.State.Health.Status}}' 2>/dev/null || echo missing)"
  if [ "$status" = "healthy" ]; then
    echo "edge-media 健康"
    echo "DEPLOYED $IMAGE"
    exit 0
  fi
  sleep 2
done

echo "edge-media 健康检查超时，最近日志：" >&2
docker logs --tail 80 edge-media >&2 || true
exit 1
