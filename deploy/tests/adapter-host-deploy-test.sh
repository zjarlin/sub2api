#!/usr/bin/env bash
# =============================================================================
# 通用主机部署执行体测试（deploy/tianjin/deploy-adapter-host-remote.sh）。
# =============================================================================
# 覆盖「换机只换参数」这条主线的关键行为：
#   1) 按目标机 .env 开关解析出 profile，并只重建 sub2api 与已启用适配器
#   2) 切镜像：只改「值里含 sub2api」的 image 行，postgres/redis 不受影响
#   3) 切镜像后的校验必须匹配本次 tag（历史上写死匹配旧写法，导致部署被误判失败）
#   4) 只启用外部服务（无本地容器）时不重建任何适配器容器
#   5) 不碰 postgres/redis：任何 compose up 都不带它们
# =============================================================================
set -euo pipefail

repo_root="$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)"
fail() { printf 'FAIL: %s\n' "$1" >&2; exit 1; }

# host_test_init <dir>：造一个隔离的目标机部署目录（含假 docker/curl）。
host_test_init() {
  local dir="$1"
  mkdir -p "$dir/deploy/cluster" "$dir/bin"
  cp "$repo_root/deploy/cluster/adapters.env.sh" "$dir/deploy/cluster/adapters.env.sh"
  cp "$repo_root/deploy/docker-compose.adapters.yml" "$dir/deploy/docker-compose.adapters.yml"
  cp "$repo_root/deploy/tianjin/deploy-adapter-host-remote.sh" "$dir/deploy-adapter-host-remote.sh"
  cat > "$dir/docker-compose.reeve.yml" <<'YML'
services:
  sub2api:
    image: sub2api:490ef827a166
  postgres:
    image: docker.1panel.live/library/postgres:18-alpine
  redis:
    image: docker.1panel.live/library/redis:8-alpine
YML
  cat > "$dir/bin/docker" <<'SH'
#!/bin/sh
printf '%s\n' "$*" >> "$MOCK_DOCKER_LOG"
case "$*" in
  "inspect sub2api --format {{.Config.Image}}") printf 'sub2api:490ef827a166\n' ;;
  "inspect sub2api --format {{.State.Health.Status}}") printf 'healthy\n' ;;
esac
exit 0
SH
  cat > "$dir/bin/curl" <<'SH'
#!/bin/sh
exit 0
SH
  chmod +x "$dir/bin/docker" "$dir/bin/curl"
}

host_test_run() {
  local dir="$1" image="$2"; shift 2
  env PATH="$dir/bin:$PATH" MOCK_DOCKER_LOG="$dir/docker.log" \
    TARGET_DEPLOY_DIR="$dir" STACK_COMPOSE=docker-compose.reeve.yml \
    SUB2API_IMAGE="$image" HEALTH_TIMEOUT=5 "$@" \
    bash "$dir/deploy-adapter-host-remote.sh" > "$dir/output.log" 2>&1
}

# ---- 场景 1：只开 WorkBuddy，切镜像并只重建 sub2api + workbuddy --------------
d1="$(mktemp -d "${TMPDIR:-/tmp}/sub2api-host-wb.XXXXXX")"
host_test_init "$d1"
printf 'SUB2API_WORKBUDDY=1\nWORKBUDDY_ADAPTER_KEY=\n' > "$d1/.env"
host_test_run "$d1" "zjarlin/sub2api:deadbeefcafe" || fail "workbuddy-only deploy failed"
grep -Fq -- 'image: zjarlin/sub2api:deadbeefcafe' "$d1/docker-compose.reeve.yml" \
  || fail "sub2api image was not switched"
grep -Fq -- 'image: docker.1panel.live/library/postgres:18-alpine' "$d1/docker-compose.reeve.yml" \
  || fail "postgres image line was rewritten"
grep -Fq -- 'image: docker.1panel.live/library/redis:8-alpine' "$d1/docker-compose.reeve.yml" \
  || fail "redis image line was rewritten"
grep -Fq -- 'profiles=workbuddy' "$d1/output.log" || fail "workbuddy profile not resolved"
grep -Fq -- 'up -d --no-deps --build sub2api-workbuddy' "$d1/docker.log" \
  || fail "workbuddy adapter was not rebuilt"
grep -Fq -- 'up -d --no-deps --no-build sub2api' "$d1/docker.log" \
  || fail "primary service was not recreated"
grep -q -- 'postgres' "$d1/docker.log" && fail "postgres was touched"
grep -q -- 'redis' "$d1/docker.log" && fail "redis was touched"
# 密钥自动生成并回写
grep -Eq '^WORKBUDDY_ADAPTER_KEY=[0-9a-f]{64}$' "$d1/.env" || fail "adapter key not generated"

# ---- 场景 2：重复部署必须幂等（同一 tag 再跑一次仍成功） ---------------------
host_test_run "$d1" "zjarlin/sub2api:deadbeefcafe" || fail "second deploy with same tag failed"
grep -Fq -- 'image: zjarlin/sub2api:deadbeefcafe' "$d1/docker-compose.reeve.yml" \
  || fail "image line drifted on repeated deploy"

# ---- 场景 3：全部关闭时只重建 sub2api，不构建任何适配器 ----------------------
d2="$(mktemp -d "${TMPDIR:-/tmp}/sub2api-host-none.XXXXXX")"
host_test_init "$d2"
printf 'SUB2API_REPLICAS=1\n' > "$d2/.env"
host_test_run "$d2" "zjarlin/sub2api:cafebabe0001" || fail "all-disabled deploy failed"
grep -Fq -- '未启用任何本地适配器' "$d2/output.log" || fail "disabled path message missing"
grep -q -- '--build' "$d2/docker.log" && fail "adapter build attempted while all disabled"
grep -Fq -- 'up -d --no-deps --no-build sub2api' "$d2/docker.log" \
  || fail "primary service recreation missing when all adapters disabled"

# ---- 场景 4：只开外部服务（laya）时不重建任何适配器容器 ----------------------
d3="$(mktemp -d "${TMPDIR:-/tmp}/sub2api-host-ext.XXXXXX")"
host_test_init "$d3"
printf 'SUB2API_LAYA=1\nBUILTIN_ADAPTER_LAYA_URL=http://edge-laya:18082\n' > "$d3/.env"
host_test_run "$d3" "zjarlin/sub2api:cafebabe0002" || fail "external-only deploy failed"
grep -Fq -- 'profiles=laya' "$d3/output.log" || fail "external profile not resolved"
grep -q -- '--build' "$d3/docker.log" && fail "external-only config built a local adapter"

# ---- 场景 5：缺镜像时必须失败，且不动 compose ------------------------------
d4="$(mktemp -d "${TMPDIR:-/tmp}/sub2api-host-noimg.XXXXXX")"
host_test_init "$d4"
printf 'SUB2API_WORKBUDDY=1\n' > "$d4/.env"
cat > "$d4/bin/docker" <<'SH'
#!/bin/sh
printf '%s\n' "$*" >> "$MOCK_DOCKER_LOG"
case "$*" in
  "image inspect "*|"inspect --format {{.Config.Image}} "*|"inspect --format {{.State.Health.Status}} "*) : ;;
  image) exit 1 ;;
esac
if [ "$1" = image ] && [ "$2" = inspect ]; then exit 1; fi
exit 0
SH
chmod +x "$d4/bin/docker"
if host_test_run "$d4" "zjarlin/sub2api:missing0000"; then
  fail "deploy succeeded although the image does not exist"
fi
grep -Fq -- 'image: sub2api:490ef827a166' "$d4/docker-compose.reeve.yml" \
  || fail "compose was modified before the image check"

for d in "$d1" "$d2" "$d3" "$d4"; do rm -rf "$d"; done
printf 'adapter host deploy test passed\n'
