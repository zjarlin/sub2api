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
SCRIPT_DIR="$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)"
source "$SCRIPT_DIR/edge-image.sh"
IMAGE_REPOSITORY="${EDGE_MEDIA_IMAGE_REPOSITORY:-zjarlin/edge-media}"
IMAGE="${EDGE_MEDIA_IMAGE:-$IMAGE_REPOSITORY:src-$(edge_image_fingerprint "$EDGE_MEDIA_DIR" '')}"
NETWORK="${SUB2API_NETWORK:-${PROJECT_NAME}_sub2api-network}"

test -f "$EDGE_MEDIA_DIR/docker/Dockerfile"
test -f "$EDGE_MEDIA_DIR/compose/docker-compose.yml"
docker network inspect "$NETWORK" >/dev/null 2>&1 || {
  echo "缺少 sub2api 网络 $NETWORK，请先部署 sub2api 集群" >&2
  exit 1
}

edge_ensure_image "$IMAGE" "$EDGE_MEDIA_DIR" -t "$IMAGE_REPOSITORY:latest"

# 服务商中继凭据：显式环境变量优先，否则读取 $DEPLOY_DIR/.env 中同名键。
# 只取白名单键值，不执行 .env 的 shell 内容。
for key in \
  PROVIDER_PEXELS_API_KEY PROVIDER_PIXABAY_API_KEY PROVIDER_COVERR_API_KEY \
  PROVIDER_OFOX_API_KEY PROVIDER_METASO_MINIMAX_API_KEY PROVIDER_MUAPI_API_KEY \
  PROVIDER_WAVESPEED_API_KEY PROVIDER_SEEDANCE_API_KEY; do
  if [ -z "${!key:-}" ] && [ -f "$DEPLOY_DIR/.env" ]; then
    value="$(awk -F= -v name="$key" '
      $1 ~ "^[[:space:]]*(export[[:space:]]+)?" name "[[:space:]]*$" {
        v=$2; sub(/#.*/, "", v); gsub(/^[[:space:]"\047]+|[[:space:]"\047]+$/, "", v); result=v
      }
      END { print result }
    ' "$DEPLOY_DIR/.env")"
    if [ -n "$value" ]; then
      export "$key=$value"
    fi
  fi
done


echo "启动边缘媒体容器（接入 $NETWORK，无宿主端口）"
# 252 只做编排与统一入口；TTS / 视频配音 / 视频生成的重模型在天津 GPU 机器上，
# 通过 MEDIA_*_UPSTREAM_URL 指向天津的 edge-media / gpt-sovits / edge-dub。
EDGE_MEDIA_IMAGE="$IMAGE" \
  EDGE_MEDIA_MODELS_DIR="${EDGE_MEDIA_MODELS_DIR:-$EDGE_MEDIA_DIR/models}" \
  EDGE_MEDIA_DATA_DIR="${EDGE_MEDIA_DATA_DIR:-$DEPLOY_DIR/data/edge-media}" \
  MEDIA_MAX_UPLOAD_BYTES="${MEDIA_MAX_UPLOAD_BYTES:-536870912}" \
  MEDIA_TTS_ENABLED="${MEDIA_TTS_ENABLED:-false}" \
  MEDIA_TTS_UPSTREAM_URL="${MEDIA_TTS_UPSTREAM_URL:-http://gpt-sovits:9880}" \
  MEDIA_TTS_REFER_WAV="${MEDIA_TTS_REFER_WAV:-}" \
  MEDIA_TTS_PROMPT_TEXT="${MEDIA_TTS_PROMPT_TEXT:-}" \
  MEDIA_TTS_PROMPT_LANGUAGE="${MEDIA_TTS_PROMPT_LANGUAGE:-zh}" \
  MEDIA_TTS_DEFAULT_LANGUAGE="${MEDIA_TTS_DEFAULT_LANGUAGE:-zh}" \
  MEDIA_DUBBING_ENABLED="${MEDIA_DUBBING_ENABLED:-true}" \
  MEDIA_DUBBING_COMMAND="${MEDIA_DUBBING_COMMAND:-}" \
  MEDIA_VIDEO_ENABLED="${MEDIA_VIDEO_ENABLED:-true}" \
  MEDIA_VIDEO_UPSTREAM_URL="${MEDIA_VIDEO_UPSTREAM_URL:-}" \
  MEDIA_VIDEO_GENERATION_ENABLED="${MEDIA_VIDEO_GENERATION_ENABLED:-false}" \
  MEDIA_VIDEO_GENERATION_UPSTREAM_URL="${MEDIA_VIDEO_GENERATION_UPSTREAM_URL:-}" \
  MEDIA_VIDEO_GENERATION_COMMAND="${MEDIA_VIDEO_GENERATION_COMMAND:-}" \
  MEDIA_TRANSCODE_COMMAND="${MEDIA_TRANSCODE_COMMAND:-}" \
  PROVIDER_PEXELS_API_KEY="${PROVIDER_PEXELS_API_KEY:-}" \
  PROVIDER_PIXABAY_API_KEY="${PROVIDER_PIXABAY_API_KEY:-}" \
  PROVIDER_COVERR_API_KEY="${PROVIDER_COVERR_API_KEY:-}" \
  PROVIDER_OFOX_API_KEY="${PROVIDER_OFOX_API_KEY:-}" \
  PROVIDER_METASO_MINIMAX_API_KEY="${PROVIDER_METASO_MINIMAX_API_KEY:-}" \
  PROVIDER_MUAPI_API_KEY="${PROVIDER_MUAPI_API_KEY:-}" \
  PROVIDER_WAVESPEED_API_KEY="${PROVIDER_WAVESPEED_API_KEY:-}" \
  PROVIDER_SEEDANCE_API_KEY="${PROVIDER_SEEDANCE_API_KEY:-}" \
  SUB2API_NETWORK="$NETWORK" \
  docker compose -f "$EDGE_MEDIA_DIR/compose/docker-compose.yml" up -d --no-build

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
