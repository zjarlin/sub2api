#!/usr/bin/env bash
#
# Mac mini 单容器部署：构建 → 数据库备份 → 候选健康预检 → 就地切流 → 复检 → 失败回滚。
#
# 拓扑：Mac mini 上 `sub2api` 由 dockge 的 compose 项目管理，容器名 sub2api，
# 直接发布宿主 18080；Postgres(pgvector)、Redis 与数据目录由现有实例提供，
# 本脚本只替换 sub2api 容器，不重建依赖，也不改动 182 上的网关。
#
# 所有主机/目录/端口都来自变量，脚本内不写死 IP：
#   APP_HOST                对外显示用主机（默认 127.0.0.1）
#   DEPLOY_DIR              compose 与资源目录（默认 ~/DockerCompose/dockge/stacks/sub2api）
#   DATA_DIR                持久化数据目录（默认 ~/DockerCompose/sub2api/data）
#   BACKUP_DIR              数据库与数据备份目录（默认 $DEPLOY_DIR/backups）
#   BACKUP_DATABASE         1 = 切流前做 pg_dump 自定义格式备份（默认 1）
#   DATABASE_CONTAINER      提供数据库的容器名（默认 pgvector）
#   DATABASE_NAME           数据库名（默认 sub2api）
#   DATABASE_USER           数据库用户（默认 postgres）
#   SUB2API_IMAGE           目标镜像（默认按 SUB2API_GIT_SHA 生成）
#   SUB2API_CONTAINER       容器名（默认 sub2api）
#   SUB2API_PORT            宿主端口（默认 18080）
#   SUB2API_BUILD           1 = 先在本机构建镜像（默认 1）
#   SUB2API_GIT_SHA         构建版本（默认取 REPO_DIR 的 HEAD）
#   CANARY_PORT             候选端口（默认自动挑空闲端口）
#   CANARY_ONLY             1 = 只起候选做预检，验证后保留候选容器并打印端口，不切流
#   DATA_DIR_MUST_MATCH     1 = DATA_DIR 必须是现役 /app/data 挂载（默认）
#   CANARY_DATABASE_NAME    隔离预演库名；DATA_DIR_MUST_MATCH=0 时必填
#   LIVE_WAIT_SECONDS       切流后健康等待秒数（默认 180）
#   EXPECTED_GIT_SHA         已构建镜像对应的提交；设置后与 REPO_DIR HEAD 不一致即拒绝部署
set -euo pipefail

log() { printf '[deploy-macmini] %s\n' "$*"; }

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="${REPO_DIR:-$(cd "$SCRIPT_DIR/../.." && pwd)}"
DEPLOY_DIR="${DEPLOY_DIR:-$HOME/DockerCompose/dockge/stacks/sub2api}"
DATA_DIR="${DATA_DIR:-$HOME/DockerCompose/sub2api/data}"
BACKUP_DIR="${BACKUP_DIR:-$DEPLOY_DIR/backups}"
CONTAINER="${SUB2API_CONTAINER:-sub2api}"
PORT="${SUB2API_PORT:-18080}"
APP_HOST="${APP_HOST:-127.0.0.1}"
IMAGE_REPOSITORY="${IMAGE_REPOSITORY:-zjarlin/sub2api}"
BUILD="${SUB2API_BUILD:-1}"
CANARY_PORT="${CANARY_PORT:-auto}"
CANARY_WAIT_SECONDS="${CANARY_WAIT_SECONDS:-300}"
LIVE_WAIT_SECONDS="${LIVE_WAIT_SECONDS:-180}"
BACKUP_DATABASE="${BACKUP_DATABASE:-1}"
DATABASE_CONTAINER="${DATABASE_CONTAINER:-pgvector}"
DATABASE_NAME="${DATABASE_NAME:-sub2api}"
DATABASE_USER="${DATABASE_USER:-postgres}"
SHA="${SUB2API_GIT_SHA:-$(git -C "$REPO_DIR" rev-parse HEAD)}"
SHORT_SHA="${SHA:0:12}"
if [ -n "${EXPECTED_GIT_SHA:-}" ] && [ "$EXPECTED_GIT_SHA" != "$SHA" ]; then
  log "expected source $EXPECTED_GIT_SHA, but SUB2API_GIT_SHA is $SHA"
  exit 1
fi
if [ -n "${EXPECTED_GIT_SHA:-}" ]; then
  REPO_SHA="$(git -C "$REPO_DIR" rev-parse HEAD)"
  [ "$REPO_SHA" = "$EXPECTED_GIT_SHA" ] || { log "REPO_DIR HEAD is $REPO_SHA, expected $EXPECTED_GIT_SHA"; exit 1; }
  log "source commit verified: $REPO_SHA"
fi
IMAGE="${SUB2API_IMAGE:-${IMAGE_REPOSITORY}:${SHORT_SHA}}"

# 远端路径必须显式存在，缺失时报错而不是猜一个旧目录。
test -f "$DEPLOY_DIR/docker-compose.yml" || { log "missing compose file: $DEPLOY_DIR/docker-compose.yml"; exit 1; }
test -d "$DATA_DIR" || { log "missing data dir: $DATA_DIR"; exit 1; }
docker inspect "$CONTAINER" >/dev/null 2>&1 || { log "live container not found: $CONTAINER"; exit 1; }
docker inspect "$DATABASE_CONTAINER" >/dev/null 2>&1 || { log "database container not found: $DATABASE_CONTAINER"; exit 1; }

# 宿主目录与容器内挂载必须一一对应，避免把备份/标记写进容器不可见的位置。
# DATA_DIR_MUST_MATCH=1（默认）用于切流；隔离预演用独立数据目录时可置 0 跳过该校验。
# 跳过时必须额外指定 CANARY_DATABASE_NAME，否则候选会连到现役库并跑迁移。
DATA_DIR_MUST_MATCH="${DATA_DIR_MUST_MATCH:-1}"
if [ "$DATA_DIR_MUST_MATCH" != "1" ]; then
  log "WARNING: DATA_DIR_MUST_MATCH=0, candidate will use $DATA_DIR instead of the live /app/data mount"
  [ -n "${CANARY_DATABASE_NAME:-}" ] || { log "CANARY_DATABASE_NAME is required when DATA_DIR_MUST_MATCH=0"; exit 1; }
  [ "${CANARY_ONLY:-0}" = "1" ] || { log "DATA_DIR_MUST_MATCH=0 is only allowed together with CANARY_ONLY=1"; exit 1; }
fi
MOUNT_SOURCE="$(docker inspect -f '{{range .Mounts}}{{if eq .Destination "/app/data"}}{{.Source}}{{end}}{{end}}' "$CONTAINER")"
test -n "$MOUNT_SOURCE" || { log "$CONTAINER has no /app/data mount"; exit 1; }
DATA_REAL="$(cd "$DATA_DIR" && pwd -P)"
MOUNT_REAL="$(cd "$MOUNT_SOURCE" && pwd -P)"
[ "$DATA_DIR_MUST_MATCH" != "1" ] || [ "$DATA_REAL" = "$MOUNT_REAL" ] || { log "DATA_DIR ($DATA_REAL) is not the live /app/data mount ($MOUNT_REAL)"; exit 1; }

if [ "$BUILD" = "1" ]; then
  log "building $IMAGE from $REPO_DIR @ $SHORT_SHA"
  docker buildx build --platform linux/arm64 --load \
    --build-arg VERSION="$SHORT_SHA" \
    --build-arg COMMIT="$SHA" \
    --build-arg DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    -t "$IMAGE" "$REPO_DIR"
else
  log "skip build (SUB2API_BUILD=0); using $IMAGE"
fi
docker image inspect "$IMAGE" >/dev/null 2>&1 || { log "image not found: $IMAGE"; exit 1; }

PREV_IMAGE_ID="$(docker inspect -f '{{.Image}}' "$CONTAINER")"
PREV_IMAGE_TAG="$(docker inspect -f '{{.Config.Image}}' "$CONTAINER")"
log "current image: $PREV_IMAGE_TAG ($PREV_IMAGE_ID)"

# 新镜像的候选进程也会执行数据库迁移，因此必须在启动候选前备份。
# 容器内本地 Unix socket 已具备 pg_dump 权限，不传输或输出数据库密码。
if [ "$BACKUP_DATABASE" = "1" ]; then
  STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
  mkdir -p "$BACKUP_DIR"
  chmod 700 "$BACKUP_DIR"
  DB_DUMP="$BACKUP_DIR/sub2api-before-deploy-$STAMP.dump"
  DB_LIST="$BACKUP_DIR/sub2api-before-deploy-$STAMP.list"
  log "backup database $DATABASE_NAME -> $DB_DUMP"
  docker exec -i "$DATABASE_CONTAINER" \
    pg_dump -U "$DATABASE_USER" -d "$DATABASE_NAME" -Fc > "$DB_DUMP"
  test -s "$DB_DUMP" || { log "database backup is empty: $DB_DUMP"; exit 1; }
  docker exec -i "$DATABASE_CONTAINER" pg_restore -l < "$DB_DUMP" > "$DB_LIST"
  test -s "$DB_LIST" || { log "database backup is not readable: $DB_DUMP"; exit 1; }
  log "database backup verified: $(wc -l < "$DB_LIST") archive entries"
fi

if [ "$CANARY_PORT" = "auto" ]; then
  CANARY_PORT="$(python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()')"
fi
CANARY_NAME="${CONTAINER}-canary-$$"

NETWORK="$(docker inspect -f '{{range $k, $v := .NetworkSettings.Networks}}{{$k}}{{end}}' "$CONTAINER")"
test -n "$NETWORK" || { log "cannot determine network for $CONTAINER"; exit 1; }

# 沿用现役容器的环境变量（跳过镜像自带的 PATH，避免覆盖镜像默认值）。
# 用临时文件而不是进程替换：busybox/受限 bash 下进程替换的输出可能读不到。
ENV_FILE="$(mktemp "${TMPDIR:-/tmp}/sub2api-deploy-env.XXXXXX")"
MOUNTS_FILE="$(mktemp "${TMPDIR:-/tmp}/sub2api-deploy-mounts.XXXXXX")"
CANARY_STARTED=0
cleanup() {
  [ "$CANARY_STARTED" = "1" ] || return 0
  docker rm -f "$CANARY_NAME" >/dev/null 2>&1 || true
}
cleanup
trap cleanup EXIT
docker inspect -f '{{range .Config.Env}}{{println .}}{{end}}' "$CONTAINER" > "$ENV_FILE"
env_args=()
while IFS= read -r kv; do
  [ -n "$kv" ] || continue
  case "$kv" in PATH=*) continue ;; esac
  env_args+=(-e "$kv")
done < "$ENV_FILE"

# 隔离预演必须连预演库，不能连接现役库（否则迁移会打到现役 schema）。
if [ "$DATA_DIR_MUST_MATCH" != "1" ]; then
  env_args+=(-e "DATABASE_DBNAME=${CANARY_DATABASE_NAME}")
fi

# 隔离预演时把 /app/data 指向独立数据目录，避免候选读写现役数据；默认沿用现役挂载。
DATA_OVERRIDE_DIR=""
if [ "$DATA_DIR_MUST_MATCH" != "1" ]; then
  DATA_OVERRIDE_DIR="$DATA_REAL"
fi

# 挂载点用 JSON 解析：docker 模板在空字段处会留空格，JSON 更可靠。
# 解析结果写入临时清单（类型|名字|源|目标），隔离预演时 /app/data 改投独立目录。
# 分隔符用 `|` 而不是 TAB：IFS 默认把 TAB/空格算空白字符并合并连续分隔，
# 名字为空时会导致字段错位（bash/BSD read 都如此）。
docker inspect -f '{{json .Mounts}}' "$CONTAINER" > "$MOUNTS_FILE" \
  || { log "docker inspect mounts failed"; exit 1; }
MOUNTS_LIST_FILE="${MOUNTS_FILE}.list"
python3 - "$MOUNTS_FILE" "$MOUNTS_LIST_FILE" "$DATA_OVERRIDE_DIR" <<'PYEOF'
import json
import sys

mounts_file, list_file, override_dir = sys.argv[1], sys.argv[2], sys.argv[3]
with open(mounts_file, encoding="utf-8") as handle:
    mounts = json.load(handle) or []
with open(list_file, "w", encoding="utf-8") as out:
    for mount in mounts:
        source = mount.get("Source") or ""
        mount_type = mount.get("Type") or ""
        destination = mount.get("Destination") or ""
        if override_dir and mount_type == "bind" and destination == "/app/data":
            source = override_dir
        out.write("|".join([mount_type, mount.get("Name") or "", source, destination]) + "\n")
PYEOF

# 只允许 bind 挂载；named volume 违反本机「一律 bind mount 到 ~/DockerCompose」的约定。
mount_flags=()
while IFS='|' read -r mtype mname msrc mdst; do
  [ -n "$mdst" ] || continue
  if [ "$mtype" != "bind" ]; then
    log "refusing to copy $mtype mount at $mdst (only bind mounts are allowed)"
    exit 1
  fi
  [ -n "$msrc" ] || { log "bind mount at $mdst has empty source"; exit 1; }
  mount_flags+=(-v "$msrc:$mdst")
done < "$MOUNTS_LIST_FILE"
[ "${#mount_flags[@]}" -gt 0 ] || { log "cannot resolve mounts for $CONTAINER"; exit 1; }

# 候选容器与现役容器使用同一网络、环境与挂载，只把端口收在 loopback。
log "starting candidate $CANARY_NAME on 127.0.0.1:$CANARY_PORT"
docker run -d --name "$CANARY_NAME" \
  --network "$NETWORK" \
  --add-host host.docker.internal:host-gateway \
  --ulimit nofile=100000:100000 \
  -p "127.0.0.1:${CANARY_PORT}:${PORT}" \
  "${env_args[@]}" \
  ${mount_flags[@]+"${mount_flags[@]}"} \
  "$IMAGE" >/dev/null
CANARY_STARTED=1

deadline=$(( $(date +%s) + CANARY_WAIT_SECONDS ))
until curl --fail --silent --max-time 5 "http://127.0.0.1:${CANARY_PORT}/health" >/dev/null 2>&1; do
  if [ "$(date +%s)" -ge "$deadline" ]; then
    log "candidate health check timed out"
    docker logs --tail 80 "$CANARY_NAME" || true
    exit 1
  fi
  if [ "$(docker inspect -f '{{.State.Status}}' "$CANARY_NAME")" != "running" ]; then
    log "candidate exited before becoming healthy"
    docker logs --tail 100 "$CANARY_NAME" || true
    exit 1
  fi
  sleep 2
done
log "candidate healthy"
if [ "${CANARY_ONLY:-0}" = "1" ]; then
  # 预检模式：保留候选容器供人工验证，不切流；用完由调用方删除。
  # 清除退出钩子，保留候选容器供验收；临时文件由当前 shell 清理。
  CANARY_STARTED=0
  trap - EXIT
  rm -f "$ENV_FILE" "$MOUNTS_FILE" "$MOUNTS_LIST_FILE"
  log "candidate-only mode: $CANARY_NAME listening on 127.0.0.1:$CANARY_PORT (live traffic untouched)"
  log "remove it with: docker rm -f $CANARY_NAME"
  exit 0
fi

# 用独立的 override 固定镜像，不修改 dockge 管理的 compose 文件本身。
OVERRIDE_FILE="$DEPLOY_DIR/docker-compose.deploy.yml"
write_override() {
  local image_tag="$1"
  cat > "$OVERRIDE_FILE" <<OVERRIDE
# 由 deploy/macmini/deploy-macmini.sh 生成：只固定 sub2api 的镜像，不改动其他服务。
services:
  ${CONTAINER}:
    image: ${image_tag}
OVERRIDE
}
write_override "$IMAGE"

compose_up() {
  local image_tag="$1"
  write_override "$image_tag"
  docker compose \
    --project-directory "$DEPLOY_DIR" \
    --project-name "$CONTAINER" \
    -f "$DEPLOY_DIR/docker-compose.yml" \
    -f "$OVERRIDE_FILE" \
    up -d --no-deps "$CONTAINER"
}

rollback() {
  log "rolling back to $PREV_IMAGE_TAG ($PREV_IMAGE_ID)"
  # 与 182 同理由：直接 rm -f，避免优雅停机把 18080 占住导致回滚起不来。
  docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
  compose_up "$PREV_IMAGE_ID" || true
  sleep 5
  docker ps --filter "name=^${CONTAINER}$" --format '{{.Names}} {{.Image}} {{.Status}}' || true
  if curl --fail --silent --max-time 10 "http://127.0.0.1:${PORT}/health" >/dev/null 2>&1; then
    log "rollback healthy (database was not restored automatically; see backup above)"
  else
    log "WARNING: rollback container is not healthy, inspect docker logs $CONTAINER"
  fi
  exit 1
}

log "switching live container to $IMAGE"
cleanup
trap - EXIT
compose_up "$IMAGE"

deadline=$(( $(date +%s) + LIVE_WAIT_SECONDS ))
until curl --fail --silent --max-time 5 "http://127.0.0.1:${PORT}/health" >/dev/null 2>&1; do
  if [ "$(date +%s)" -ge "$deadline" ]; then
    log "live health check failed after switch"
    docker logs --tail 100 "$CONTAINER" || true
    rollback
  fi
  if [ "$(docker inspect -f '{{.State.Status}}' "$CONTAINER")" != "running" ]; then
    log "live container exited after switch"
    docker logs --tail 100 "$CONTAINER" || true
    rollback
  fi
  sleep 2
done

deadline=$(( $(date +%s) + LIVE_WAIT_SECONDS ))
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
