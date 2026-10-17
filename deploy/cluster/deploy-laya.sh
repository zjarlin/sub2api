#!/usr/bin/env bash
set -euo pipefail

DEPLOY_DIR="${DEPLOY_DIR:-/opt/sub2api}"
SCRIPT_DIR="$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)"
source "$SCRIPT_DIR/edge-image.sh"
EDGE_LAYA_DIR="$DEPLOY_DIR/edge-laya"
IMAGE_REPOSITORY="${EDGE_LAYA_IMAGE_REPOSITORY:-zjarlin/edge-laya}"
PYTHON_IMAGE="${EDGE_LAYA_PYTHON_IMAGE:-python:3.12-slim-bookworm}"
IMAGE="${EDGE_LAYA_IMAGE:-$IMAGE_REPOSITORY:src-$(edge_image_fingerprint "$EDGE_LAYA_DIR" "PYTHON_IMAGE=$PYTHON_IMAGE")}"
MODEL_DIR="${EDGE_LAYA_MODELS_DIR:-$DEPLOY_DIR/edge-laya/models}"
NETWORK="${SUB2API_NETWORK:-sub2api_sub2api-network}"

for file in \
  "$MODEL_DIR/checkpoint/model.safetensors" \
  "$MODEL_DIR/checkpoint/rl_agent_config.json" \
  "$MODEL_DIR/checkpoint/multilingual/model.safetensors" \
  "$MODEL_DIR/checkpoint/multilingual/rl_agent_config.json"; do
  test -s "$file" || { echo "缺少 Laya 权重文件: $file" >&2; exit 1; }
done
docker network inspect "$NETWORK" >/dev/null
# 国内网络下 Docker Hub 常不可达，允许通过环境变量替换基础镜像源。
edge_ensure_image "$IMAGE" "$EDGE_LAYA_DIR" --build-arg "PYTHON_IMAGE=$PYTHON_IMAGE"
EDGE_LAYA_IMAGE="$IMAGE" EDGE_LAYA_MODELS_DIR="$MODEL_DIR" SUB2API_NETWORK="$NETWORK" \
  docker compose -f "$EDGE_LAYA_DIR/compose/docker-compose.yml" up -d --no-build
for _ in $(seq 1 60); do
  if [ "$(docker inspect edge-laya --format '{{.State.Health.Status}}' 2>/dev/null || true)" = healthy ]; then
    echo "DEPLOYED $IMAGE"
    exit 0
  fi
  sleep 5
done
docker logs --tail 50 edge-laya >&2 || true
exit 1
