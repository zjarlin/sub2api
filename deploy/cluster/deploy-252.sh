#!/usr/bin/env bash

set -euo pipefail

DEPLOY_DIR="${DEPLOY_DIR:-/opt/sub2api}"
PROJECT_NAME="${PROJECT_NAME:-sub2api}"
IMAGE="${SUB2API_IMAGE:?SUB2API_IMAGE is required}"
REPLICAS="${SUB2API_REPLICAS:-1}"
CANARY_PORT="${CANARY_PORT:-auto}"
CANARY_WAIT_SECONDS="${CANARY_WAIT_SECONDS:-180}"
CANARY_OBSERVE_SECONDS="${CANARY_OBSERVE_SECONDS:-20}"
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

# 码道（华为云 CodeArts 代码智能体 Web 端）适配器独立启用。
MADAO_ENABLED="${SUB2API_MADAO:-}"
if [ -z "$MADAO_ENABLED" ] && [ -f "$DEPLOY_DIR/.env" ]; then
  MADAO_ENABLED="$(awk -F= '
    $1 ~ /^[[:space:]]*(export[[:space:]]+)?SUB2API_MADAO[[:space:]]*$/ {
      value=$2
      sub(/#.*/, "", value)
      gsub(/[[:space:]"\047]/, "", value)
      result=value
    }
    END { if (result == "1") print "1"; else print "0" }
  ' "$DEPLOY_DIR/.env")"
fi
if [ "$MADAO_ENABLED" = "1" ]; then
  test -f "$DEPLOY_DIR/deploy/docker-compose.madao.yml"
  COMPOSE+=(-f "$DEPLOY_DIR/deploy/docker-compose.madao.yml")
fi

# Cursor 独立启用，真实账号 Key 在管理页面保存，部署仅生成内部共享密钥。
CURSOR_ENABLED="${SUB2API_CURSOR:-}"
if [ -z "$CURSOR_ENABLED" ] && [ -f "$DEPLOY_DIR/.env" ]; then
  CURSOR_ENABLED="$(awk -F= '
    $1 ~ /^[[:space:]]*(export[[:space:]]+)?SUB2API_CURSOR[[:space:]]*$/ {
      value=$2
      sub(/#.*/, "", value)
      gsub(/[[:space:]"\047]/, "", value)
      result=value
    }
    END { if (result == "1") print "1"; else print "0" }
  ' "$DEPLOY_DIR/.env")"
fi
if [ "$CURSOR_ENABLED" = "1" ]; then
  test -f "$DEPLOY_DIR/deploy/docker-compose.cursor.yml"
  COMPOSE+=(-f "$DEPLOY_DIR/deploy/docker-compose.cursor.yml")
fi

# Windsurf 独立启用，真实账号 Token 在管理页面保存，部署仅生成内部共享密钥。
WINDSURF_ENABLED="${SUB2API_WINDSURF:-}"
if [ -z "$WINDSURF_ENABLED" ] && [ -f "$DEPLOY_DIR/.env" ]; then
  WINDSURF_ENABLED="$(awk -F= '
    $1 ~ /^[[:space:]]*(export[[:space:]]+)?SUB2API_WINDSURF[[:space:]]*$/ {
      value=$2
      sub(/#.*/, "", value)
      gsub(/[[:space:]"\047]/, "", value)
      result=value
    }
    END { if (result == "1") print "1"; else print "0" }
  ' "$DEPLOY_DIR/.env")"
fi
if [ "$WINDSURF_ENABLED" = "1" ]; then
  test -f "$DEPLOY_DIR/deploy/docker-compose.windsurf.yml"
  COMPOSE+=(-f "$DEPLOY_DIR/deploy/docker-compose.windsurf.yml")
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
ARENA_PROXY_ENABLED=0
if [ "$ARENA_ENABLED" = "1" ]; then
  test -f "$DEPLOY_DIR/deploy/docker-compose.arena.yml"
  COMPOSE+=(-f "$DEPLOY_DIR/deploy/docker-compose.arena.yml" --profile arena)

  # 专属代理只随 Arena 启用；显式环境变量优先于私有 .env。
  ARENA_PROXY_ENABLED="${SUB2API_ARENA_PROXY:-}"
  if [ -z "$ARENA_PROXY_ENABLED" ] && [ -f "$DEPLOY_DIR/.env" ]; then
    ARENA_PROXY_ENABLED="$(awk -F= '
      $1 ~ /^[[:space:]]*(export[[:space:]]+)?SUB2API_ARENA_PROXY[[:space:]]*$/ {
        value=$2
        sub(/#.*/, "", value)
        gsub(/[[:space:]"\047]/, "", value)
        result=value
      }
      END { if (result == "1") print "1"; else print "0" }
    ' "$DEPLOY_DIR/.env")"
  fi
  if [ "$ARENA_PROXY_ENABLED" = "1" ]; then
    test -f "$DEPLOY_DIR/deploy/docker-compose.arena-proxy.yml"
    COMPOSE+=(-f "$DEPLOY_DIR/deploy/docker-compose.arena-proxy.yml")
  fi
fi

# OpenAI OAuth 专用美国出口：显式开启后叠加编排文件；缺失立即报错。
OPENAI_EGRESS_ENABLED="${SUB2API_OPENAI_EGRESS:-}"
if [ -z "$OPENAI_EGRESS_ENABLED" ] && [ -f "$DEPLOY_DIR/.env" ]; then
  OPENAI_EGRESS_ENABLED="$(awk -F= '
    $1 ~ /^[[:space:]]*(export[[:space:]]+)?SUB2API_OPENAI_EGRESS[[:space:]]*$/ {
      value=$2
      sub(/#.*/, "", value)
      gsub(/[[:space:]"\047]/, "", value)
      result=value
    }
    END { if (result == "1") print "1"; else print "0" }
  ' "$DEPLOY_DIR/.env")"
fi
if [ "$OPENAI_EGRESS_ENABLED" = "1" ]; then
  test -f "$DEPLOY_DIR/deploy/docker-compose.openai-egress.yml"
  COMPOSE+=(-f "$DEPLOY_DIR/deploy/docker-compose.openai-egress.yml")
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
CANARY_COMPOSE=("${COMPOSE[@]}" --profile canary)
NGINX_CONFIG="$DEPLOY_DIR/deploy/cluster/nginx.conf"

if [ ! -f "$NGINX_CONFIG" ]; then
  echo "Missing gateway config: $NGINX_CONFIG" >&2
  exit 1
fi

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
if [ "$MADAO_ENABLED" = "1" ]; then
  ensure_adapter_key MADAO_ADAPTER_KEY
fi
if [ "$CURSOR_ENABLED" = "1" ]; then
  ensure_adapter_key CURSOR_ADAPTER_KEY
fi
if [ "$WINDSURF_ENABLED" = "1" ]; then
  ensure_adapter_key WINDSURF_ADAPTER_KEY
fi
if [ "$ARENA_ENABLED" = "1" ]; then
  ensure_adapter_key ARENA_AGENT_BRIDGE_KEY
fi
docker image inspect "$IMAGE" > "$RELEASE_DIR/image.json"

CURRENT_IMAGE="$(docker ps --filter 'label=com.docker.compose.service=sub2api' --format '{{.Image}}' | head -n1)"
if [ -n "$CURRENT_IMAGE" ]; then
  printf '%s\n' "$CURRENT_IMAGE" > "$RELEASE_DIR/previous-image"
fi

CURRENT_PRIMARY_CONTAINER="$(docker ps --filter 'label=com.docker.compose.service=sub2api' --format '{{.Names}}' | head -n1)"
LEGACY_PRIMARY_CONTAINER=""
if [ -z "$CURRENT_PRIMARY_CONTAINER" ] && docker ps --format '{{.Names}}' | grep -qx 'sub2api'; then
  LEGACY_PRIMARY_CONTAINER="sub2api"
fi
BASE_CONTAINER="${CURRENT_PRIMARY_CONTAINER:-$LEGACY_PRIMARY_CONTAINER}"
if [ -z "$CURRENT_IMAGE" ] && [ -n "$BASE_CONTAINER" ]; then
  CURRENT_IMAGE="$(docker inspect --format '{{.Config.Image}}' "$BASE_CONTAINER")"
  printf '%s\n' "$CURRENT_IMAGE" > "$RELEASE_DIR/previous-image"
fi

CUTOVER_STARTED=0
CANDIDATE_SERVING=0
PRIMARY_SERVING=0
CANARY_CONTAINER=""

gateway_running() {
  docker inspect --format '{{.State.Running}}' sub2api-gateway 2>/dev/null | grep -qx true
}

route_to() {
  local target="$1"
  case "$target" in
    sub2api|sub2api-canary) ;;
    *) echo "Unsupported gateway target: $target" >&2; return 1 ;;
  esac
  local tmp backup
  tmp="$(mktemp "$NGINX_CONFIG.tmp.XXXXXX")"
  backup="$RELEASE_DIR/nginx.conf.before-route"
  if [ ! -f "$backup" ]; then
    cp "$NGINX_CONFIG" "$backup"
  fi
  sed -E "s#http://(sub2api|sub2api-canary):8080#http://$target:8080#g" "$NGINX_CONFIG" > "$tmp"
  if ! grep -Fq "http://$target:8080" "$tmp"; then
    rm -f "$tmp"
    echo "Gateway config has no matching backend target" >&2
    return 1
  fi
  # Preserve the bind-mounted inode so an already-running gateway sees the update.
  cat "$tmp" > "$NGINX_CONFIG"
  rm -f "$tmp"
  if ! docker exec sub2api-gateway nginx -t >/dev/null; then
    cat "$backup" > "$NGINX_CONFIG"
    echo "Gateway config validation failed" >&2
    return 1
  fi
  if ! docker exec sub2api-gateway nginx -s reload >/dev/null; then
    cat "$backup" > "$NGINX_CONFIG"
    docker exec sub2api-gateway nginx -s reload >/dev/null 2>&1 || true
    echo "Gateway reload failed" >&2
    return 1
  fi
}

wait_for_url() {
  local url="$1"
  local attempts="$2"
  local i
  for ((i=1; i<=attempts; i++)); do
    if curl --fail --silent --show-error --max-time 5 "$url" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  return 1
}

wait_for_gateway_backend() {
  local target="$1"
  local attempts="$2"
  local i
  for ((i=1; i<=attempts; i++)); do
    if docker exec sub2api-gateway wget -q -T 5 -O /dev/null "http://$target:8080/ready" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  return 1
}

stop_canary() {
  local ids id
  if [ -n "$CANARY_CONTAINER" ]; then
    docker stop "$CANARY_CONTAINER" >/dev/null 2>&1 || true
    docker rm -f "$CANARY_CONTAINER" >/dev/null 2>&1 || true
  fi
  ids="$(docker ps -aq --filter 'label=sub2api.role=release-candidate')"
  for id in $ids; do
    docker stop "$id" >/dev/null 2>&1 || true
    docker rm -f "$id" >/dev/null 2>&1 || true
  done
}

start_canary() {
  local env_file network publish
  case "$CANARY_PORT" in
    auto|"") publish="127.0.0.1::8080" ;;
    *) publish="127.0.0.1:$CANARY_PORT:8080" ;;
  esac
  CANARY_CONTAINER="sub2api-canary-$STAMP"
  stop_canary
  if [ -n "$BASE_CONTAINER" ]; then
    env_file="$(mktemp "$RELEASE_DIR/canary.env.XXXXXX")"
    chmod 0600 "$env_file"
    docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "$BASE_CONTAINER" > "$env_file"
    network="$(docker inspect --format '{{range $name,$network := .NetworkSettings.Networks}}{{println $name}}{{end}}' "$BASE_CONTAINER" | head -n1)"
    test -n "$network"
  else
    env_file="$DEPLOY_DIR/.env"
    network="${PROJECT_NAME}_sub2api-network"
  fi
  echo "Starting isolated candidate $CANARY_CONTAINER behind $publish"
  docker run -d --name "$CANARY_CONTAINER" \
    --network "$network" \
    --network-alias sub2api-canary \
    --env-file "$env_file" \
    --volumes-from "$BASE_CONTAINER" \
    --publish "$publish" \
    --env SERVER_HOST=0.0.0.0 \
    --env SERVER_PORT=8080 \
    --env LOG_OUTPUT_FILE_PATH="$CANARY_LOG_PATH" \
    --restart no \
    --label "sub2api.role=release-candidate" \
    "$IMAGE"
  [ "$env_file" = "$DEPLOY_DIR/.env" ] || rm -f "$env_file"
  CANARY_PORT="$(docker inspect --format '{{(index (index .NetworkSettings.Ports "8080/tcp") 0).HostPort}}' "$CANARY_CONTAINER")"
  test -n "$CANARY_PORT"
  echo "Candidate listening on 127.0.0.1:$CANARY_PORT"
}
rollback() {
  local exit_status=$?
  trap - ERR
  "${COMPOSE[@]}" ps -a > "$RELEASE_DIR/failed-containers" 2>&1 || true
  if [ -n "$CANARY_CONTAINER" ]; then docker logs --tail 200 "$CANARY_CONTAINER" > "$RELEASE_DIR/canary.log" 2>&1 || true; fi
  "${COMPOSE[@]}" logs --no-color --tail 200 sub2api gateway > "$RELEASE_DIR/failed-startup.log" 2>&1 || true
  echo "Deployment diagnostics: $RELEASE_DIR/failed-startup.log"
  cat "$RELEASE_DIR/failed-containers" "$RELEASE_DIR/canary.log" "$RELEASE_DIR/failed-startup.log" || true
  if gateway_running; then
    if [ "$PRIMARY_SERVING" = "1" ]; then
      route_to sub2api || true
    elif [ "$CANDIDATE_SERVING" = "1" ]; then
      echo "Deployment failed after cutover; keeping the verified candidate serving"
      route_to sub2api-canary || true
    elif [ -n "$BASE_CONTAINER" ] && docker inspect "$BASE_CONTAINER" >/dev/null 2>&1; then
      route_to sub2api || true
    fi
  fi
  if [ "$CANDIDATE_SERVING" != "1" ] || [ "$PRIMARY_SERVING" = "1" ]; then
    stop_canary
  fi
  return "$exit_status"
}
trap rollback ERR

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
if [ "$MADAO_ENABLED" = "1" ]; then
  echo "Building and starting CodeArts (码道) adapter"
  "${COMPOSE[@]}" up -d --build --wait --wait-timeout 180 sub2api-madao
fi
if [ "$CURSOR_ENABLED" = "1" ]; then
  echo "Building and starting Cursor SDK adapter"
  "${COMPOSE[@]}" up -d --build --wait --wait-timeout 180 sub2api-cursor
fi
if [ "$WINDSURF_ENABLED" = "1" ]; then
  echo "Building and starting Windsurf adapter"
  "${COMPOSE[@]}" up -d --build --wait --wait-timeout 180 sub2api-windsurf
fi
if [ "$ARENA_ENABLED" = "1" ]; then
  if [ "$ARENA_PROXY_ENABLED" = "1" ]; then
    echo "Starting dedicated Arena proxy"
    "${COMPOSE[@]}" up -d --wait --wait-timeout 180 sub2api-arena-proxy
  fi
  echo "Building and starting Arena adapter"
  "${COMPOSE[@]}" up -d --build sub2api-arena
fi

if [ "$OPENAI_EGRESS_ENABLED" = "1" ]; then
  echo "Starting OpenAI OAuth US egress proxy"
  "${COMPOSE[@]}" up -d --wait --wait-timeout 180 sub2api-openai-egress
fi

# The existing gateway process stays alive. If it is already serving, pin it to
# the current primary while the candidate starts behind a loopback-only port.
if [ -n "$CURRENT_PRIMARY_CONTAINER" ] && gateway_running; then
  route_to sub2api
  wait_for_url "http://127.0.0.1:18080/ready" 30
fi

CANARY_LOG_PATH="/app/data/logs/sub2api-canary.log"
mkdir -p "$DEPLOY_DIR/logs"
start_canary
wait_for_url "http://127.0.0.1:$CANARY_PORT/ready" "$CANARY_WAIT_SECONDS"
if ! curl --fail --silent --show-error --max-time 20 "http://127.0.0.1:$CANARY_PORT/health" >/dev/null; then
  echo "Candidate health endpoint failed" >&2
  false
fi
if ! curl --fail --silent --show-error --max-time 20 "http://127.0.0.1:$CANARY_PORT/" >/dev/null; then
  echo "Candidate frontend endpoint failed" >&2
  false
fi

# First migration from the old direct listener has an unavoidable short handoff,
# but it happens only after the new candidate is already healthy.
if [ -n "$LEGACY_PRIMARY_CONTAINER" ]; then
  echo "Migrating legacy direct listener after candidate verification"
  docker stop "$LEGACY_PRIMARY_CONTAINER" >/dev/null || true
  docker rm "$LEGACY_PRIMARY_CONTAINER" >/dev/null || true
fi
if ! gateway_running; then
  "${COMPOSE[@]}" up -d --wait --wait-timeout 180 --no-deps gateway
fi

# Point 18080 at the verified candidate.
CUTOVER_STARTED=1
route_to sub2api-canary
if ! wait_for_url "http://127.0.0.1:18080/ready" 60; then
  echo "Gateway cutover probe failed" >&2
  false
fi
if ! curl --fail --silent --show-error --max-time 20 http://127.0.0.1:18080/health >/dev/null; then
  echo "Gateway cutover health probe failed" >&2
  false
fi
CANDIDATE_SERVING=1

# Keep the candidate on 18080 while the replacement primary starts. The old
# primary remains available during the observe window for a fast rollback.
sleep "$CANARY_OBSERVE_SECONDS"
wait_for_url "http://127.0.0.1:18080/ready" 30
if [ -n "$CURRENT_PRIMARY_CONTAINER" ]; then
  "${COMPOSE[@]}" stop sub2api >/dev/null 2>&1 || true
  "${COMPOSE[@]}" rm -f sub2api >/dev/null 2>&1 || true
fi
SUB2API_IMAGE="$IMAGE" "${COMPOSE[@]}" up -d --wait --wait-timeout "$CANARY_WAIT_SECONDS" --no-deps sub2api

# Verify the replacement primary through the stable gateway container before
# moving traffic back from the candidate.
wait_for_gateway_backend sub2api 60
route_to sub2api
if ! wait_for_url "http://127.0.0.1:18080/ready" 60; then
  echo "Promoted primary readiness probe failed" >&2
  false
fi
if ! curl --fail --silent --show-error --max-time 20 http://127.0.0.1:18080/health >/dev/null; then
  echo "Promoted primary health probe failed" >&2
  false
fi
PRIMARY_SERVING=1
stop_canary
if docker ps --filter "label=sub2api.role=release-candidate" --filter status=running --format '{{.ID}}' | grep -q .; then
  echo "Warning: a release candidate is still running; next deployment will clean it" >&2
fi

docker ps --filter "label=com.docker.compose.project=$PROJECT_NAME" --format '{{.Names}} {{.Status}}' > "$RELEASE_DIR/containers"
printf '%s\n' "$IMAGE" > "$DEPLOY_DIR/DEPLOYED_IMAGE"
printf '%s\n' "$STAMP" > "$DEPLOY_DIR/DEPLOYED_RELEASE"
trap - ERR
echo "DEPLOYED $IMAGE replicas=1 release=$STAMP"
