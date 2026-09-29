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

# 显式开启后叠加豆包、TRAE Work、WorkBuddy 与 ZCode 内置服务；编排文件缺失立即报错。
if [ "$BUILTIN_ADAPTERS_ENABLED" = "1" ]; then
  test -f "$DEPLOY_DIR/deploy/docker-compose.builtin-adapters.yml"
  COMPOSE+=(-f "$DEPLOY_DIR/deploy/docker-compose.builtin-adapters.yml")
fi

# DeepSeek 网页适配器独立启用，不要求同时启动其他内置服务。
DEEPSEEK_WEB_ENABLED="${SUB2API_DEEPSEEK_WEB:-}"
if [ -z "$DEEPSEEK_WEB_ENABLED" ] && [ -f "$DEPLOY_DIR/.env" ]; then
  DEEPSEEK_WEB_ENABLED="$(awk -F= '
    $1 ~ /^[[:space:]]*(export[[:space:]]+)?SUB2API_DEEPSEEK_WEB[[:space:]]*$/ {
      value=$2
      sub(/#.*/, "", value)
      gsub(/[[:space:]"\047]/, "", value)
      result=value
    }
    END { if (result == "1") print "1"; else print "0" }
  ' "$DEPLOY_DIR/.env")"
fi
if [ "$DEEPSEEK_WEB_ENABLED" = "1" ]; then
  test -f "$DEPLOY_DIR/deploy/docker-compose.deepseek-web.yml"
  COMPOSE+=(-f "$DEPLOY_DIR/deploy/docker-compose.deepseek-web.yml")
fi

# Arena 独立启用，只启动私网文本适配器；真实登录与专属会话另行配置。
ARENA_ENABLED="${SUB2API_ARENA:-}"
if [ -z "$ARENA_ENABLED" ] && [ -f "$DEPLOY_DIR/.env" ]; then
  ARENA_ENABLED="$(awk -F= '
    $1 ~ /^[[:space:]]*(export[[:space:]]+)?SUB2API_ARENA[[:space:]]*$/ {
      value=$2
      sub(/#.*/, "", value)
      gsub(/[[:space:]"\047]/, "", value)
      result=value
    }
    END { if (result == "1") print "1"; else print "0" }
  ' "$DEPLOY_DIR/.env")"
fi
if [ "$ARENA_ENABLED" = "1" ]; then
  test -f "$DEPLOY_DIR/deploy/docker-compose.arena.yml"
  COMPOSE+=(-f "$DEPLOY_DIR/deploy/docker-compose.arena.yml" --profile arena)
fi

# 只读取编排开关，不执行 .env 中的 shell 内容；显式环境变量优先。
EDGE_MEDIA_ENABLED="${SUB2API_EDGE_MEDIA:-}"
if [ -z "$EDGE_MEDIA_ENABLED" ] && [ -f "$DEPLOY_DIR/.env" ]; then
  EDGE_MEDIA_ENABLED="$(awk -F= '
    $1 ~ /^[[:space:]]*(export[[:space:]]+)?SUB2API_EDGE_MEDIA[[:space:]]*$/ {
      value=$2
      sub(/#.*/, "", value)
      gsub(/[[:space:]"\047]/, "", value)
      result=value
    }
    END { if (result == "1") print "1"; else print "0" }
  ' "$DEPLOY_DIR/.env")"
fi

# 显式开启后叠加 GATEWAY_MEDIA_* / GATEWAY_VISION_*；编排文件缺失立即报错。
if [ "$EDGE_MEDIA_ENABLED" = "1" ]; then
  test -f "$DEPLOY_DIR/deploy/docker-compose.edge-media.yml"
  COMPOSE+=(-f "$DEPLOY_DIR/deploy/docker-compose.edge-media.yml")
fi

cd "$DEPLOY_DIR"
mkdir -p releases

STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
RELEASE_DIR="$DEPLOY_DIR/releases/$STAMP"
mkdir -p "$RELEASE_DIR"

if [ -f docker-compose.override.yml ]; then
  cp docker-compose.override.yml "$RELEASE_DIR/docker-compose.override.yml"
fi

# 首次部署自动生成内部共享密钥，并持久化供后续重建复用。
# 上游套餐凭据独立配置，不能用共享密钥替代。
ensure_adapter_key() {
  local key_name="$1"
  if [ -n "${!key_name:-}" ]; then
    return
  fi
  local key_present
  key_present="$(awk -F= -v key="$key_name" '
    $1 ~ "^[[:space:]]*" key "[[:space:]]*$" {
      value=substr($0, index($0, "=")+1)
      gsub(/[[:space:]"\047]/, "", value)
      present=(value != "")
    }
    END { print present ? "1" : "0" }
  ' "$DEPLOY_DIR/.env")"
  if [ "$key_present" != "1" ]; then
    (umask 077; cp "$DEPLOY_DIR/.env" "$RELEASE_DIR/env.before-$key_name")
    local new_adapter_key
    new_adapter_key="$(openssl rand -hex 32)"
    printf '\n%s=%s\n' "$key_name" "$new_adapter_key" >> "$DEPLOY_DIR/.env"
  fi
  unset "$key_name"
}
if [ "$BUILTIN_ADAPTERS_ENABLED" = "1" ]; then
  ensure_adapter_key ZCODE_ADAPTER_KEY
  ensure_adapter_key VIBEX_ADAPTER_KEY
fi
if [ "$DEEPSEEK_WEB_ENABLED" = "1" ]; then
  ensure_adapter_key DEEPSEEK_WEB_ADAPTER_KEY
fi
if [ "$ARENA_ENABLED" = "1" ]; then
  ensure_adapter_key ARENA_AGENT_BRIDGE_KEY
fi
docker image inspect "$IMAGE" > "$RELEASE_DIR/image.json"

CURRENT_IMAGE="$(docker ps --filter 'label=com.docker.compose.service=sub2api' --format '{{.Image}}' | head -n1)"
if [ -n "$CURRENT_IMAGE" ]; then
  printf '%s\n' "$CURRENT_IMAGE" > "$RELEASE_DIR/previous-image"
fi

rollback() {
  local exit_status=$?
  trap - ERR
  # 回滚会重建失败容器，先保留启动日志和状态供 TeamCity 与现场诊断。
  "${COMPOSE[@]}" ps -a > "$RELEASE_DIR/failed-containers" 2>&1 || true
  "${COMPOSE[@]}" logs --no-color --tail 200 sub2api gateway > "$RELEASE_DIR/failed-startup.log" 2>&1 || true
  echo "Deployment diagnostics: $RELEASE_DIR/failed-startup.log"
  cat "$RELEASE_DIR/failed-containers" "$RELEASE_DIR/failed-startup.log" || true
  if [ -n "$CURRENT_IMAGE" ] && docker image inspect "$CURRENT_IMAGE" >/dev/null 2>&1; then
    echo "Deployment failed; restoring $CURRENT_IMAGE"
    if SUB2API_IMAGE="$CURRENT_IMAGE" "${COMPOSE[@]}" up -d --wait --wait-timeout 180 --no-deps --scale "sub2api=$REPLICAS" sub2api gateway; then
      echo "Rollback healthy: $CURRENT_IMAGE"
    else
      echo "Rollback failed: $CURRENT_IMAGE" >&2
    fi
  fi
  return "$exit_status"
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
  echo "Building and starting built-in adapters (doubao / traework / workbuddy / vibex / zcode)"
  "${COMPOSE[@]}" up -d --build sub2api-desktop sub2api-traework sub2api-workbuddy sub2api-vibex sub2api-zcode
fi
if [ "$DEEPSEEK_WEB_ENABLED" = "1" ]; then
  echo "Building and starting DeepSeek web adapter"
  "${COMPOSE[@]}" up -d --build sub2api-deepseek-web
fi
if [ "$ARENA_ENABLED" = "1" ]; then
  echo "Building and starting Arena adapter"
  "${COMPOSE[@]}" up -d --build sub2api-arena
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
