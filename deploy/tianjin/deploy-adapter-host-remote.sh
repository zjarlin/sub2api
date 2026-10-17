#!/usr/bin/env bash
# =============================================================================
# 执行体：在「目标机」上切换镜像、按 .env 开关启动内置适配器并做健康检查。
# =============================================================================
# 由 deploy-adapter-host.sh 通过本机或 SSH 调用；不单独对外使用。
#
# 输入（环境变量）：
#   TARGET_DEPLOY_DIR  部署目录（含 .env、compose 文件、deploy/）
#   STACK_COMPOSE      主编排文件名（默认 docker-compose.reeve.yml）
#   SUB2API_IMAGE      要部署的镜像 tag（必须已存在本机）
#   SKIP_BUILD=1       不构建适配器
#   HEALTH_TIMEOUT     等待 sub2api healthy 的秒数（默认 180）
# =============================================================================
set -euo pipefail

TARGET_DEPLOY_DIR="${TARGET_DEPLOY_DIR:-/opt/reeve/stacks/sub2api}"
STACK_COMPOSE="${STACK_COMPOSE:-docker-compose.reeve.yml}"
SUB2API_IMAGE="${SUB2API_IMAGE:?SUB2API_IMAGE is required}"
SKIP_BUILD="${SKIP_BUILD:-0}"
HEALTH_TIMEOUT="${HEALTH_TIMEOUT:-180}"
ADAPTER_COMPOSE="deploy/docker-compose.adapters.yml"

cd "$TARGET_DEPLOY_DIR"
ENV_FILE="$TARGET_DEPLOY_DIR/.env"

[ -f "$ENV_FILE" ] || { echo "缺少 $ENV_FILE" >&2; exit 1; }
[ -f "$ADAPTER_COMPOSE" ] || { echo "缺少 $ADAPTER_COMPOSE（需要同步部署目录）" >&2; exit 1; }
[ -f "$STACK_COMPOSE" ] || { echo "缺少 $STACK_COMPOSE" >&2; exit 1; }
docker image inspect "$SUB2API_IMAGE" >/dev/null 2>&1 || { echo "镜像不存在: $SUB2API_IMAGE" >&2; exit 1; }

source "$TARGET_DEPLOY_DIR/deploy/cluster/adapters.env.sh"

echo "==> 1/5 解析启用的适配器"
resolve_enabled_adapters "$ENV_FILE"
PROFILES="$(adapter_profiles_csv)"
echo "    profiles=${PROFILES:-<none>}"
if [ "${#ADAPTER_URL_EXPORTS[@]}" -gt 0 ]; then
  for kv in "${ADAPTER_URL_EXPORTS[@]}"; do [ -n "$kv" ] && echo "    $kv"; done
fi

echo "==> 2/5 补齐共享密钥（首次部署自动生成）"
if [ "${#ADAPTER_KEY_VARS[@]}" -gt 0 ]; then
  for key in "${ADAPTER_KEY_VARS[@]}"; do
    ensure_env_key "$key" "$ENV_FILE" "$TARGET_DEPLOY_DIR/releases"
  done
fi

echo "==> 3/5 切换 sub2api 镜像"
CURRENT="$(docker inspect sub2api --format '{{.Config.Image}}' 2>/dev/null || true)"
if [ -n "$CURRENT" ]; then
  printf '%s\n' "$CURRENT" > "$TARGET_DEPLOY_DIR/.previous-image"
  echo "    回滚镜像: $CURRENT"
fi
if grep -qE '^[[:space:]]*image:[[:space:]]*sub2api:' "$STACK_COMPOSE"; then
  sed -i "s|^\([[:space:]]*image:[[:space:]]*\)sub2api:.*|\1$SUB2API_IMAGE|" "$STACK_COMPOSE"
else
  echo "compose 缺少 sub2api 镜像行，无法切换镜像" >&2
  exit 1
fi
grep -nE '^[[:space:]]*image:[[:space:]]*sub2api:' "$STACK_COMPOSE"

# 组装 compose 调用：基础栈 + 适配器叠加层；profile 决定适配器开关。
export COMPOSE_PROFILES="$PROFILES"
if [ "${#ADAPTER_URL_EXPORTS[@]}" -gt 0 ]; then
  for kv in "${ADAPTER_URL_EXPORTS[@]}"; do [ -n "$kv" ] && export "$kv"; done
fi
COMPOSE=(docker compose --project-name sub2api --env-file "$ENV_FILE" -f "$STACK_COMPOSE" -f "$ADAPTER_COMPOSE")

echo "==> 4/5 校验 compose 配置"
"${COMPOSE[@]}" config >/dev/null

echo "==> 5/5 重建服务"
# 只动 sub2api 与已启用的适配器：
#   - 不碰 postgres/redis。目标机的数据库容器名可能由历史部署决定
#     （例如天津实际叫 pg/redis，而 compose 里写的是 sub2api-postgres），
#     按 compose 拉起会新建一个空库容器，必须避免。
#   - sub2api 一律 --no-build：镜像由流水线构建后传入，避免在本机误重建。
if [ -n "$PROFILES" ] && [ "${#ADAPTER_SERVICES[@]}" -gt 0 ]; then
  if [ "$SKIP_BUILD" = "1" ]; then
    echo "    启动适配器 ${ADAPTER_SERVICES[*]}（profiles=$PROFILES，跳过构建）"
    "${COMPOSE[@]}" up -d --no-deps --no-build "${ADAPTER_SERVICES[@]}"
  else
    echo "    构建并启动适配器 ${ADAPTER_SERVICES[*]}（profiles=$PROFILES）"
    "${COMPOSE[@]}" up -d --no-deps --build "${ADAPTER_SERVICES[@]}"
  fi
else
  echo "    未启用任何本地适配器"
fi

# 主服务最后重建，确保拿到最新镜像与已注入的 BUILTIN_ADAPTER_* 环境变量。
"${COMPOSE[@]}" up -d --no-deps --no-build sub2api

echo "==> 等待 sub2api 健康"
for i in $(seq 1 "$HEALTH_TIMEOUT"); do
  st="$(docker inspect sub2api --format '{{.State.Health.Status}}' 2>/dev/null || echo missing)"
  if [ "$st" = "healthy" ]; then echo "    healthy after ${i}s"; break; fi
  if [ "$st" = "missing" ]; then echo "    sub2api 容器不存在" >&2; exit 1; fi
  sleep 1
done
final="$(docker inspect sub2api --format '{{.State.Health.Status}}' 2>/dev/null || echo missing)"
[ "$final" = "healthy" ] || { echo "sub2api 未在 ${HEALTH_TIMEOUT}s 内 healthy（当前 $final）" >&2; exit 1; }

echo "==> 容器状态"
docker ps --filter name=sub2api --format '{{.Names}} | {{.Image}} | {{.Status}}'
echo "DEPLOYED $SUB2API_IMAGE profiles=${PROFILES:-<none>}"
