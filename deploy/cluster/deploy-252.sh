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

# 内置适配器：由 deploy/cluster/adapters.env.sh 的平台注册表统一解析。
# 每个平台一个 .env 开关（SUB2API_<PLATFORM>=1），profile 由开关计算得出，
# 不再为每个平台单独维护 -f 叠加清单。
source "$DEPLOY_DIR/deploy/cluster/adapters.env.sh"
ADAPTER_COMPOSE="$DEPLOY_DIR/deploy/docker-compose.adapters.yml"
if [ -f "$ADAPTER_COMPOSE" ]; then
  COMPOSE+=(-f "$ADAPTER_COMPOSE")
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

# 显式开启后叠加 GATEWAY_MEDIA_* / GATEWAY_VISION_* / GATEWAY_MAIL_*；编排文件缺失立即报错。
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
# 容器内由目录挂载提供的配置路径；与 compose 的 command -c 保持一致。
GATEWAY_CONF="/etc/nginx/cluster/nginx.conf"

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
# 按注册表为已启用平台补齐共享密钥（首次部署生成并回写 .env）。
resolve_enabled_adapters "$DEPLOY_DIR/.env"
if [ -f "$ADAPTER_COMPOSE" ] && [ "${#ADAPTER_KEY_VARS[@]}" -gt 0 ]; then
  for _key in "${ADAPTER_KEY_VARS[@]}"; do
    ensure_env_key "$_key" "$DEPLOY_DIR/.env" "$RELEASE_DIR"
  done
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
  # 先确保网关挂载到当前布局；否则 -c 路径不存在，reload 会直接失败（
  # 包括回滚路径）。这里只做「挂载是否可用」的判断，不做内容比较。
  ensure_gateway_mount_current
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
  # 配置来自目录挂载（/etc/nginx/cluster/nginx.conf），按路径解析，
  # 因此直接覆盖宿主文件后 reload 即可被运行中的网关读到。
  cat "$tmp" > "$NGINX_CONFIG"
  rm -f "$tmp"
  if ! docker exec sub2api-gateway nginx -t -c "$GATEWAY_CONF" >/dev/null; then
    cat "$backup" > "$NGINX_CONFIG"
    echo "Gateway config validation failed" >&2
    return 1
  fi
  if ! docker exec sub2api-gateway nginx -s reload -c "$GATEWAY_CONF" >/dev/null; then
    cat "$backup" > "$NGINX_CONFIG"
    docker exec sub2api-gateway nginx -s reload -c "$GATEWAY_CONF" >/dev/null 2>&1 || true
    echo "Gateway reload failed" >&2
    return 1
  fi
}

# 网关容器必须挂载到当前布局（目录挂载 + -c /etc/nginx/cluster/nginx.conf）。
# 旧容器挂的是单个 nginx.conf 文件（inode 绑定），git archive 替换文件后它
# 永远读不到新内容；reload 也修不了挂载本身，只能重建容器。
# 判断依据是「容器内目标路径是否存在且当前挂载生效」，而不是内容 md5——
# route_to 会按需改动宿主文件，用内容比较会导致无谓重建。
ensure_gateway_mount_current() {
  if gateway_running && docker exec sub2api-gateway test -f "$GATEWAY_CONF" 2>/dev/null; then
    return 0
  fi
  echo "Gateway uses a stale mount layout; recreating gateway container"
  "${COMPOSE[@]}" up -d --wait --wait-timeout 180 --no-deps gateway
  if ! docker exec sub2api-gateway test -f "$GATEWAY_CONF" 2>/dev/null; then
    echo "Gateway did not expose $GATEWAY_CONF after recreate" >&2
    return 1
  fi
}

# 部署收尾断言：网关加载的配置必须与宿主一致，且边缘路由确实生效。
# 缺少这一步时，配置变更失效只会表现为「部署成功但接口 404」，很难被发现。
verify_gateway_config_applied() {
  local host_md5 container_md5
  host_md5="$(md5sum "$NGINX_CONFIG" | awk '{print $1}')"
  container_md5="$(docker exec sub2api-gateway md5sum "$GATEWAY_CONF" 2>/dev/null | awk '{print $1}')"
  if [ "$host_md5" != "$container_md5" ]; then
    echo "Gateway config mismatch after deploy (container=$container_md5 host=$host_md5)" >&2
    return 1
  fi
  # 边缘叠加层（SUB2API_EDGE_MEDIA=1）会同时开放 /mail/、/vision/、/media/。
  # 已启用的边缘路由必须落在 Go 后端（鉴权会返回 401/4xx），
  # 而不是落到前端 SPA（返回 200 且 content-type 为 text/html）。
  if [ "${EDGE_MEDIA_ENABLED:-0}" = "1" ]; then
    local ctype
    ctype="$(curl --silent --show-error --max-time 15 -o /dev/null -w '%{content_type}' \
      -X POST "http://127.0.0.1:18080/mail/messages" -H 'Content-Type: application/json' -d '{}' || true)"
    case "$ctype" in
      *text/html*)
        echo "Gateway /mail/ route is not reaching the backend (content-type=$ctype)" >&2
        return 1
        ;;
    esac
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
  # 适配器地址以显式 --env 传入：首次部署（无既有容器可 inspect）时 .env 里
  # 没有 BUILTIN_ADAPTER_*_URL，docker run 也不会自动继承宿主环境变量。
  local -a ADAPTER_ENV_ARGS=()
  if [ "${#ADAPTER_URL_EXPORTS[@]}" -gt 0 ]; then
    local _kv
    for _kv in "${ADAPTER_URL_EXPORTS[@]}"; do
      [ -n "$_kv" ] && ADAPTER_ENV_ARGS+=(--env "$_kv")
    done
  fi
  echo "Starting isolated candidate $CANARY_CONTAINER behind $publish"
  docker run -d --name "$CANARY_CONTAINER" \
    --network "$network" \
    --network-alias sub2api-canary \
    --env-file "$env_file" \
    ${ADAPTER_ENV_ARGS[@]+"${ADAPTER_ENV_ARGS[@]}"} \
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

# 统一按注册表启动内置适配器：profile 由 .env 开关计算，URL/KEY 注入主服务。
# 与旧逻辑等价（旧的每个平台一个 -f 叠加文件），但单一数据源、换机只换开关。
ADAPTER_PROFILES_CSV="$(adapter_profiles_csv)"
ADAPTER_PROXY_ENABLED="${SUB2API_ARENA_PROXY:-}"
if [ -z "$ADAPTER_PROXY_ENABLED" ] && [ -f "$DEPLOY_DIR/.env" ]; then
  ADAPTER_PROXY_ENABLED="$(read_env_flag SUB2API_ARENA_PROXY "$DEPLOY_DIR/.env")"
fi
if [ "$ADAPTER_PROXY_ENABLED" = "1" ]; then
  ADAPTER_PROFILES_CSV="${ADAPTER_PROFILES_CSV:+$ADAPTER_PROFILES_CSV,}arena-proxy"
fi
EGRESS_ENABLED="${SUB2API_OPENAI_EGRESS:-}"
if [ -z "$EGRESS_ENABLED" ] && [ -f "$DEPLOY_DIR/.env" ]; then
  EGRESS_ENABLED="$(read_env_flag SUB2API_OPENAI_EGRESS "$DEPLOY_DIR/.env")"
fi
if [ "$EGRESS_ENABLED" = "1" ]; then
  ADAPTER_PROFILES_CSV="${ADAPTER_PROFILES_CSV:+$ADAPTER_PROFILES_CSV,}openai-egress"
fi
export COMPOSE_PROFILES="$ADAPTER_PROFILES_CSV"
# 空数组在 set -u 下展开会报错，先判断长度。
if [ "${#ADAPTER_URL_EXPORTS[@]}" -gt 0 ]; then
  for _kv in "${ADAPTER_URL_EXPORTS[@]}"; do
    [ -n "$_kv" ] && export "$_kv"
  done
fi

# 只启动适配器服务本身：绝不能在这里不带服务名 `up`，否则会连带重建
# sub2api 主容器与 gateway，直接破坏下面的蓝绿切换。
ADAPTER_UP_SERVICES=()
if [ "${#ADAPTER_SERVICES[@]}" -gt 0 ]; then
  ADAPTER_UP_SERVICES+=("${ADAPTER_SERVICES[@]}")
fi
if [ "$ADAPTER_PROXY_ENABLED" = "1" ]; then
  ADAPTER_UP_SERVICES+=(sub2api-arena-proxy)
fi
if [ "$EGRESS_ENABLED" = "1" ]; then
  ADAPTER_UP_SERVICES+=(sub2api-openai-egress)
fi

if [ "${#ADAPTER_UP_SERVICES[@]}" -gt 0 ]; then
  echo "Building and starting built-in adapters (profiles=$ADAPTER_PROFILES_CSV): ${ADAPTER_UP_SERVICES[*]}"
  # --no-deps：适配器的 depends_on: sub2api 会把主服务一起带上，
  # 那样会在候选验证前就重建主容器，破坏蓝绿。
  "${COMPOSE[@]}" up -d --build --wait --wait-timeout 180 --no-deps "${ADAPTER_UP_SERVICES[@]}"
else
  echo "No built-in adapters enabled"
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
# 确保网关容器加载当前配置布局（旧的单文件 inode 挂载无法自愈）。
ensure_gateway_mount_current

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
# 失败关闭：确认网关确实在跑最新配置，而不是又一次静默停留在旧文件上。
verify_gateway_config_applied
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
