#!/usr/bin/env bash

set -euo pipefail

DEPLOY_DIR="${DEPLOY_DIR:-/opt/sub2api}"
PROJECT_NAME="${PROJECT_NAME:-sub2api}"
IMAGE="${SUB2API_IMAGE:?SUB2API_IMAGE is required}"
REPLICAS="${SUB2API_REPLICAS:-2}"
CANARY_REPLICAS="${CANARY_REPLICAS:-1}"
COMPOSE=(docker compose --project-name "$PROJECT_NAME" --project-directory "$DEPLOY_DIR" --env-file "$DEPLOY_DIR/.env" -f "$DEPLOY_DIR/deploy/docker-compose.yml" -f "$DEPLOY_DIR/docker-compose.override.yml" -f "$DEPLOY_DIR/deploy/cluster/docker-compose.yml")

# 只读取编排开关，不执行 .env 中的 shell 内容；显式环境变量优先。
BUILTIN_ADAPTERS_ENABLED="${SUB2API_BUILTIN_ADAPTERS:-}"
if [ -z "$BUILTIN_ADAPTERS_ENABLED" ] && [ -f "$DEPLOY_DIR/.env" ]; then
  BUILTIN_ADAPTERS_ENABLED="$(awk -F= '
    $1 ~ /^[[:space:]]*(export[[:space:]]+)?SUB2API_BUILTIN_ADAPTERS[[:space:]]*$/ {
      value=$2
      sub(/#.*/, "", value)
      gsub(/[[:space:]"\047]/, "", value)
      result=value
    }
    END { if (result == "1") print "1"; else print "0" }
  ' "$DEPLOY_DIR/.env")"
fi

# 显式开启后叠加豆包、TRAE Work 与 WorkBuddy 内置服务；编排文件缺失立即报错。
if [ "$BUILTIN_ADAPTERS_ENABLED" = "1" ]; then
  test -f "$DEPLOY_DIR/deploy/docker-compose.builtin-adapters.yml"
  COMPOSE+=(-f "$DEPLOY_DIR/deploy/docker-compose.builtin-adapters.yml")
fi

cd "$DEPLOY_DIR"
mkdir -p releases

STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
RELEASE_DIR="$DEPLOY_DIR/releases/$STAMP"
mkdir -p "$RELEASE_DIR"

if [ -f docker-compose.override.yml ]; then
  cp docker-compose.override.yml "$RELEASE_DIR/docker-compose.override.yml"
fi
docker image inspect "$IMAGE" > "$RELEASE_DIR/image.json"

CURRENT_IMAGE="$(docker ps --filter 'label=com.docker.compose.service=sub2api' --format '{{.Image}}' | head -n1)"
if [ -n "$CURRENT_IMAGE" ]; then
  printf '%s\n' "$CURRENT_IMAGE" > "$RELEASE_DIR/previous-image"
fi

rollback() {
  if [ -n "$CURRENT_IMAGE" ] && docker image inspect "$CURRENT_IMAGE" >/dev/null 2>&1; then
    echo "Deployment failed; restoring $CURRENT_IMAGE"
    SUB2API_IMAGE="$CURRENT_IMAGE" "${COMPOSE[@]}" up -d --no-deps --scale "sub2api=$REPLICAS" sub2api gateway || true
  fi
}
trap rollback ERR

if docker ps --format '{{.Names}}' | grep -qx sub2api; then
  echo "Migrating the existing single-container listener to the stable gateway"
  "${COMPOSE[@]}" stop sub2api || true
  "${COMPOSE[@]}" rm -f sub2api || true
fi

export SUB2API_IMAGE="$IMAGE"
"${COMPOSE[@]}" config >/dev/null
"${COMPOSE[@]}" up -d --no-recreate postgres redis
if [ "$BUILTIN_ADAPTERS_ENABLED" = "1" ]; then
  echo "Building and starting built-in adapters (doubao / traework / workbuddy)"
  "${COMPOSE[@]}" up -d --build sub2api-desktop sub2api-traework sub2api-workbuddy
fi
echo "Starting canary with replicas=$CANARY_REPLICAS"
"${COMPOSE[@]}" up -d --wait --wait-timeout 180 --no-deps --scale "sub2api=$CANARY_REPLICAS" sub2api gateway

curl --fail --silent --show-error --max-time 20 http://127.0.0.1:18080/health >/dev/null
echo "Canary healthy; scaling to replicas=$REPLICAS"
"${COMPOSE[@]}" up -d --wait --wait-timeout 180 --no-deps --scale "sub2api=$REPLICAS" sub2api gateway
curl --fail --silent --show-error --max-time 20 http://127.0.0.1:18080/health >/dev/null
docker ps --filter "label=com.docker.compose.project=$PROJECT_NAME" --format '{{.Names}} {{.Status}}' > "$RELEASE_DIR/containers"
printf '%s\n' "$IMAGE" > "$DEPLOY_DIR/DEPLOYED_IMAGE"
printf '%s\n' "$STAMP" > "$DEPLOY_DIR/DEPLOYED_RELEASE"
trap - ERR
echo "DEPLOYED $IMAGE replicas=$REPLICAS release=$STAMP"
