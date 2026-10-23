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

log() { printf '[deploy-gateway] %s\n' "$*"; }

COMPOSE_FILE="$DEPLOY_DIR/docker-compose.yml"
test -f "$COMPOSE_FILE" || { log "missing compose file: $COMPOSE_FILE"; exit 1; }

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

cleanup() { docker rm -f "$CANARY_NAME" >/dev/null 2>&1 || true; }
trap cleanup EXIT

rollback() {
  log "rolling back to $PREV_IMAGE_TAG ($PREV_IMAGE_ID)"
  SUB2API_IMAGE="$PREV_IMAGE_ID" docker compose \
    --project-directory "$DEPLOY_DIR" --project-name "$CONTAINER" \
    -f "$COMPOSE_FILE" up -d --no-deps "$CONTAINER" || true
  sleep 5
  docker ps --filter "name=^${CONTAINER}$" --format '{{.Names}} {{.Image}} {{.Status}}' || true
  exit 1
}

NETWORK="$(docker inspect -f '{{range $k, $v := .NetworkSettings.Networks}}{{$k}}{{end}}' "$CONTAINER")"

# 沿用现役容器的环境变量（跳过镜像自带的 PATH，避免覆盖镜像默认值）。
env_args=()
while IFS= read -r kv; do
  [ -n "$kv" ] || continue
  case "$kv" in PATH=*) continue ;; esac
  env_args+=(-e "$kv")
done < <(docker inspect -f '{{range .Config.Env}}{{println .}}{{end}}' "$CONTAINER")
# 用 JSON 解析挂载点：docker 的格式化模板在空字段处会留下空格，
# 逐行 + 分隔符拼接容易被空白污染。
mount_flags=()
while IFS=$'\t' read -r mtype mname msrc mdst; do
  [ -n "$mdst" ] || continue
  if [ "$mtype" = "volume" ]; then mount_flags+=(-v "$mname:$mdst"); else mount_flags+=(-v "$msrc:$mdst"); fi
done < <(docker inspect -f '{{json .Mounts}}' "$CONTAINER" | python3 -c '
import json, sys
for m in json.load(sys.stdin) or []:
    print("\t".join([m.get("Type") or "", m.get("Name") or "", m.get("Source") or "", m.get("Destination") or ""]))
')

log "starting candidate $CANARY_NAME on 127.0.0.1:$CANARY_PORT"
docker run -d --name "$CANARY_NAME" \
  --network "$NETWORK" \
  -p "127.0.0.1:${CANARY_PORT}:8080" \
  "${env_args[@]}" \
  "${mount_flags[@]}" \
  "$IMAGE" >/dev/null

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
log "candidate healthy; switching live container"
cleanup; trap - EXIT

SUB2API_IMAGE="$IMAGE" docker compose \
  --project-directory "$DEPLOY_DIR" --project-name "$CONTAINER" \
  -f "$COMPOSE_FILE" up -d --no-deps "$CONTAINER"

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
