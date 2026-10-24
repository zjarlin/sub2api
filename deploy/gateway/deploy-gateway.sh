#!/usr/bin/env bash
#
# 182 单容器网关：构建 → 候选预检 → 就地替换 → 健康复检 → 失败自动回滚。
#
# 拓扑说明：182 上只有一个 `sub2api` 容器，由它自己发布宿主 18080；没有 nginx
# gateway、没有副本、没有常驻 canary。这与 252 时代的 deploy/cluster/deploy-252.sh
# （nginx 网关 + 蓝绿副本）是两套拓扑，**不要**把那份脚本拿到 182 上跑：它会在
# 18080 上再起一个 nginx 跟现役容器抢端口。
#
# 所有主机/端口/目录都来自变量，脚本内不写死 IP：
#   APP_HOST            对外主机（默认 127.0.0.1，仅用于打印入口）
#   DEPLOY_DIR          线上目录（compose + 数据），默认 /root/DockerCompose/sub2api
#   SUB2API_IMAGE       目标镜像（默认按 SUB2API_GIT_SHA 生成）
#   SUB2API_CONTAINER   容器/项目名，默认 sub2api
#   SUB2API_PORT        宿主端口，默认 18080
#   SUB2API_BUILD       1 = 先构建镜像（默认 1）
#   SUB2API_GIT_SHA     构建版本（默认取 REPO_DIR 的 HEAD；镜像标签取前 12 位）
#   CANARY_PORT         候选端口（默认自动挑空闲端口）
#   CANARY_ONLY         1 = 只起候选做预检，验证后保留候选容器并打印端口，不切流
#   ADAPTER_ENV_FILE    适配器开关与共享密钥（默认 $DEPLOY_DIR/adapters/.env）
#   ADAPTER_COMPOSE_FILE 适配器叠加层（默认空 = 自动找 $REPO_DIR/deploy/docker-compose.adapters.yml）
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="${REPO_DIR:-$(cd "$SCRIPT_DIR/../.." && pwd)}"
DEPLOY_DIR="${DEPLOY_DIR:-/root/DockerCompose/sub2api}"
CONTAINER="${SUB2API_CONTAINER:-sub2api}"
PORT="${SUB2API_PORT:-18080}"
APP_HOST="${APP_HOST:-127.0.0.1}"
IMAGE_REPOSITORY="${IMAGE_REPOSITORY:-zjarlin/sub2api}"
BUILD="${SUB2API_BUILD:-1}"
CANARY_PORT="${CANARY_PORT:-auto}"
CANARY_WAIT_SECONDS="${CANARY_WAIT_SECONDS:-240}"
SHA="${SUB2API_GIT_SHA:-$(git -C "$REPO_DIR" rev-parse HEAD)}"
SHORT_SHA="${SHA:0:12}"
IMAGE="${SUB2API_IMAGE:-${IMAGE_REPOSITORY}:${SHORT_SHA}}"
ADAPTER_ENV_FILE="${ADAPTER_ENV_FILE:-$DEPLOY_DIR/adapters/.env}"
ADAPTER_COMPOSE_FILE="${ADAPTER_COMPOSE_FILE-$REPO_DIR/deploy/docker-compose.adapters.yml}"

log() { printf '[deploy-gateway] %s\n' "$*"; }

COMPOSE_FILE="$DEPLOY_DIR/docker-compose.yml"
test -f "$COMPOSE_FILE" || { log "missing compose file: $COMPOSE_FILE"; exit 1; }

# ---------------------------------------------------------------------------
# 内置适配器：开关与共享密钥只在 $ADAPTER_ENV_FILE 里，主服务要「感知」某平台
# 可用，靠 BUILTIN_ADAPTER_<X>_URL/_KEY 同时非空且 BUILTIN_ADAPTER_ENABLED=true。
# 这些只在进程启动时读一次（config.LoadForBootstrap），所以换 env 必须重建容器，
# 这里把同一份计算结果同时喂给候选 docker run 和 compose 切换，避免两边不一致。
# ---------------------------------------------------------------------------
ADAPTER_COMPOSE_FILES=()
ADAPTER_COMPOSE_ENV_ARGS=()
adapter_env_args=()
adapter_up_env=()
if [ -n "$ADAPTER_COMPOSE_FILE" ]; then
  test -f "$ADAPTER_COMPOSE_FILE" || { log "missing adapter compose file: $ADAPTER_COMPOSE_FILE"; exit 1; }
  test -f "$ADAPTER_ENV_FILE" || { log "missing adapter env file: $ADAPTER_ENV_FILE"; exit 1; }
  # shellcheck source=../cluster/adapters.env.sh
  source "$REPO_DIR/deploy/cluster/adapters.env.sh"
  resolve_enabled_adapters "$ADAPTER_ENV_FILE"
  ADAPTER_COMPOSE_FILES=(-f "$ADAPTER_COMPOSE_FILE")
  # 共享密钥随 --env-file 进入 compose 的变量替换；进程环境里的 SOURCE 变量优先，
  # 所以未启用平台仍然是显式空串，不会被 .env 残留值污染。
  ADAPTER_COMPOSE_ENV_ARGS=(--env-file "$ADAPTER_ENV_FILE")
  adapter_env_args+=(--env "BUILTIN_ADAPTER_ENABLED=true")
  for _uv in ${ADAPTER_URLVARS[@]+"${ADAPTER_URLVARS[@]}"}; do
    adapter_env_args+=(--env "$_uv")
    adapter_up_env+=("$_uv")
  done
  # 候选容器是 docker run，不经过 compose 的 env 替换，共享密钥必须显式传入，
  # 否则候选能连上适配器却会被适配器 401 拒绝，预检形同虚设。
  # 密钥按平台映射：BUILTIN_ADAPTER_WORKBUDDY_URL -> BUILTIN_ADAPTER_WORKBUDDY_KEY，
  # 值来自 .env 里注册表登记的 <PLATFORM>_ADAPTER_KEY。
  for _uv in ${ADAPTER_URLVARS[@]+"${ADAPTER_URLVARS[@]}"}; do
    _url_var="${_uv%%=*}"
    _key_var="$(adapter_key_var_for_url_var "$_url_var")" || { log "no key var for $_url_var"; exit 1; }
    _value="$(read_env_value "$_key_var" "$ADAPTER_ENV_FILE")"
    [ -n "$_value" ] || { log "missing adapter key $_key_var in $ADAPTER_ENV_FILE"; exit 1; }
    adapter_env_args+=(--env "${_url_var%_URL}_KEY=$_value")
  done
  for _kv in ${ADAPTER_URL_EXPORTS[@]+"${ADAPTER_URL_EXPORTS[@]}"}; do
    adapter_up_env+=("$_kv")
  done
  [ -n "$(adapter_profiles_csv)" ] || { log "adapter env enables no platform: $ADAPTER_ENV_FILE"; exit 1; }
  log "adapters enabled: $(adapter_profiles_csv)"
else
  log "no adapter overlay; live container env is reused as-is"
fi

if [ "$BUILD" = "1" ]; then
  log "building $IMAGE from $REPO_DIR @ $SHORT_SHA"
  docker buildx build --load \
    --build-arg VERSION="$SHORT_SHA" \
    --build-arg COMMIT="$SHA" \
    --build-arg DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    -t "$IMAGE" "$REPO_DIR"
else
  log "skip build (SUB2API_BUILD=0); using $IMAGE"
fi
docker image inspect "$IMAGE" >/dev/null 2>&1 || { log "image not found: $IMAGE"; exit 1; }

docker inspect "$CONTAINER" >/dev/null 2>&1 || { log "live container not found: $CONTAINER"; exit 1; }
PREV_IMAGE_ID="$(docker inspect -f '{{.Image}}' "$CONTAINER")"
PREV_IMAGE_TAG="$(docker inspect -f '{{.Config.Image}}' "$CONTAINER")"
log "current image: $PREV_IMAGE_TAG ($PREV_IMAGE_ID)"

if [ "$CANARY_PORT" = "auto" ]; then
  CANARY_PORT="$(python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()')"
fi
CANARY_NAME="${CONTAINER}-canary-$$"

# mount_work 记录候选挂载计划（见下方 mount-plan.sh）；和候选容器一起清理，
# 失败退出时也不留临时目录。
mount_work=""
cleanup() {
  docker rm -f "$CANARY_NAME" >/dev/null 2>&1 || true
  [ -n "$mount_work" ] && rm -rf "$mount_work"
}
trap cleanup EXIT

NETWORK="$(docker inspect -f '{{range $k, $v := .NetworkSettings.Networks}}{{$k}}{{end}}' "$CONTAINER")"

# 沿用现役容器的环境变量（跳过镜像自带的 PATH，避免覆盖镜像默认值）。
env_args=()
while IFS= read -r kv; do
  [ -n "$kv" ] || continue
  case "$kv" in PATH=*) continue ;; esac
  env_args+=(-e "$kv")
done < <(docker inspect -f '{{range .Config.Env}}{{println .}}{{end}}' "$CONTAINER")
# 复用现役容器的挂载点（/app/data 等）。解析放在 deploy/gateway/mount-plan.sh：
# 那里用 Python 直接产出结果文件，避免 shell 逐字段 read 时因空字段/连续分隔符
# 把 bind 挂载整条丢掉（历史缺陷：候选通过了健康检查，切流后却丢了 /app/data）。
# shellcheck source=./mount-plan.sh
source "$SCRIPT_DIR/mount-plan.sh"
mount_work="$(mktemp -d "${TMPDIR:-/tmp}/sub2api-mount-plan.XXXXXX")"
docker inspect -f '{{json .Mounts}}' "$CONTAINER" > "$mount_work/mounts.json" \
  || { log "docker inspect mounts failed"; exit 1; }
write_mount_plan "$mount_work/mounts.json" "$mount_work/specs" "$mount_work/dests" \
  || { log "failed to build mount plan"; exit 1; }
mount_flags=()
while IFS= read -r spec; do
  [ -n "$spec" ] || continue
  mount_flags+=(-v "$spec")
done < "$mount_work/specs"
mount_destinations=()
while IFS= read -r dest; do
  [ -n "$dest" ] || continue
  mount_destinations+=("$dest")
done < "$mount_work/dests"
rm -rf "$mount_work"; mount_work=""
if [ "${#mount_destinations[@]}" -eq 0 ]; then
  log "current container reports no mounts; continuing without mount replication"
fi

log "starting candidate $CANARY_NAME on 127.0.0.1:$CANARY_PORT"
docker run -d --name "$CANARY_NAME" \
  --network "$NETWORK" \
  -p "127.0.0.1:${CANARY_PORT}:8080" \
  "${env_args[@]}" \
  ${adapter_env_args[@]+"${adapter_env_args[@]}"} \
  ${mount_flags[@]+"${mount_flags[@]}"} \
  "$IMAGE" >/dev/null

# 候选必须挂到与现役相同的路径集合：少挂 /app/data 这类目录时
# 候选健康检查仍会通过，但切流后业务数据目录就丢了，必须在这里拦住。
canary_dests="$(docker inspect -f '{{json .Mounts}}' "$CANARY_NAME" | python3 -c '
import json, sys
print(",".join(sorted(m.get("Destination") or "" for m in json.load(sys.stdin) or [])))
')"
expected_dests="$(printf '%s\n' ${mount_destinations[@]+"${mount_destinations[@]}"} | sed '/^$/d' | sort | paste -sd, -)"
if [ "$canary_dests" != "$expected_dests" ]; then
  log "candidate mount mismatch: expected [$expected_dests] got [$canary_dests]"
  docker rm -f "$CANARY_NAME" >/dev/null 2>&1 || true
  trap - EXIT
  exit 1
fi

deadline=$(( $(date +%s) + CANARY_WAIT_SECONDS ))
until curl --fail --silent --max-time 5 "http://127.0.0.1:${CANARY_PORT}/health" >/dev/null 2>&1; do
  if [ "$(date +%s)" -ge "$deadline" ]; then
    log "candidate health check timed out"
    docker logs --tail 40 "$CANARY_NAME" || true
    exit 1
  fi
  if [ "$(docker inspect -f '{{.State.Status}}' "$CANARY_NAME")" != "running" ]; then
    log "candidate exited before becoming healthy"
    docker logs --tail 60 "$CANARY_NAME" || true
    exit 1
  fi
  sleep 2
done
log "candidate healthy"
if [ "${CANARY_ONLY:-0}" = "1" ]; then
  # 预检模式：保留候选容器供人工验证（env / 接口），不切流。用完由调用方删除。
  trap - EXIT
  log "candidate-only mode: $CANARY_NAME listening on 127.0.0.1:$CANARY_PORT (live traffic untouched)"
  log "remove it with: docker rm -f $CANARY_NAME"
  exit 0
fi
log "switching live container"
cleanup; trap - EXIT

compose_up() {
  env ${adapter_up_env[@]+"${adapter_up_env[@]}"} SUB2API_IMAGE="$1" docker compose \
    --project-directory "$DEPLOY_DIR" --project-name "$CONTAINER" \
    -f "$COMPOSE_FILE" ${ADAPTER_COMPOSE_FILES[@]+"${ADAPTER_COMPOSE_FILES[@]}"} \
    ${ADAPTER_COMPOSE_ENV_ARGS[@]+"${ADAPTER_COMPOSE_ENV_ARGS[@]}"} \
    up -d --no-deps "$CONTAINER"
}

# 失败回滚要覆盖「切换前/切换后」两种情况：前者 18080 仍是旧容器，compose up
# 就是原地恢复；后者已被新容器占住，必须等新容器退出后再起（配置没变所以不会
# 被重新创建），否则容器名冲突，回滚本身也会失败。
# 用 `docker rm -f` 而不是 stop：安全开关会把 SIGTERM 拖满 120s，而 compose 的
# 18080 端口绑定在创建时就已经释放。
rollback() {
  log "rolling back to $PREV_IMAGE_TAG ($PREV_IMAGE_ID)"
  docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
  compose_up "$PREV_IMAGE_ID" || true
  sleep 5
  docker ps --filter "name=^${CONTAINER}$" --format '{{.Names}} {{.Image}} {{.Status}}' || true
  curl --fail --silent --max-time 10 "http://127.0.0.1:${PORT}/health" >/dev/null 2>&1 \
    && log "rollback healthy" \
    || log "WARNING: rollback container is not healthy, inspect docker logs $CONTAINER"
  exit 1
}

compose_up "$IMAGE"

deadline=$(( $(date +%s) + 120 ))
until curl --fail --silent --max-time 5 "http://127.0.0.1:${PORT}/health" >/dev/null 2>&1; do
  if [ "$(date +%s)" -ge "$deadline" ]; then
    log "live health check failed after switch"
    docker logs --tail 60 "$CONTAINER" || true
    rollback
  fi
  sleep 2
done

# 容器刚重建时 docker 的健康状态还是 starting，这里等它变 healthy 再落标记。
deadline=$(( $(date +%s) + 120 ))
until [ "$(docker ps --filter "name=^${CONTAINER}$" --filter health=healthy -q | wc -l | tr -d ' ')" = "1" ]; do
  if [ "$(date +%s)" -ge "$deadline" ]; then
    log "container did not reach healthy state"
    docker inspect -f '{{.State.Health.Status}}' "$CONTAINER" || true
    rollback
  fi
  sleep 3
done
printf '%s\n' "$IMAGE" > "$DEPLOY_DIR/DEPLOYED_IMAGE"
printf '%s\n' "$(date -u +%Y%m%dT%H%M%SZ)" > "$DEPLOY_DIR/DEPLOYED_RELEASE"
log "deployed $IMAGE (rollback image kept: $PREV_IMAGE_ID)"
log "endpoint http://${APP_HOST}:${PORT}/"
