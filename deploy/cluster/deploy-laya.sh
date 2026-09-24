#!/usr/bin/env bash
set -euo pipefail

DEPLOY_DIR="${DEPLOY_DIR:-/opt/sub2api}"
IMAGE="${EDGE_LAYA_IMAGE:?EDGE_LAYA_IMAGE is required}"
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
docker build -t "$IMAGE" -f "$DEPLOY_DIR/edge-laya/docker/Dockerfile" "$DEPLOY_DIR/edge-laya"
EDGE_LAYA_IMAGE="$IMAGE" EDGE_LAYA_MODELS_DIR="$MODEL_DIR" SUB2API_NETWORK="$NETWORK" \
  docker compose -f "$DEPLOY_DIR/edge-laya/compose/docker-compose.yml" up -d --force-recreate
for _ in $(seq 1 60); do
  if [ "$(docker inspect edge-laya --format '{{.State.Health.Status}}' 2>/dev/null || true)" = healthy ]; then
    echo "DEPLOYED $IMAGE"
    exit 0
  fi
  sleep 5
done
docker logs --tail 50 edge-laya >&2 || true
exit 1
