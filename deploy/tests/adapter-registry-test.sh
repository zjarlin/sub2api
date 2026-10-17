#!/usr/bin/env bash
# =============================================================================
# 内置适配器统一编排测试（替代旧的 per-adapter 叠加测试）。
# =============================================================================
# 覆盖 deploy/cluster/adapters.env.sh 注册表 + deploy-252.sh 的新接入方式：
#   1) 单平台开关 -> 只启用该 profile，其他平台不启动
#   2) 旧开关 SUB2API_BUILTIN_ADAPTERS=1 -> 兼容映射到 5 个本地适配器
#   3) 统一叠加层 docker-compose.adapters.yml 被选中（不再逐平台 -f）
#   4) 共享密钥自动生成、持久化、不回显
#   5) 适配器健康失败 -> 不启动候选
#   6) 外部服务（laya/jev）仅注入地址、无本地容器
#   7) 未配置地址的外部服务视为关闭
# =============================================================================
set -euo pipefail

repo_root="$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)"
# shellcheck source=deploy/tests/adapter-deploy-lib.sh
source "$repo_root/deploy/tests/adapter-deploy-lib.sh"

fail() { printf 'FAIL: %s\n' "$1" >&2; exit 1; }

# ---- 场景 1：只开 WorkBuddy -------------------------------------------------
t1="$(mktemp -d "${TMPDIR:-/tmp}/sub2api-adapter-wb.XXXXXX")"
adapter_test_init "$repo_root" "$t1"
printf 'SUB2API_WORKBUDDY=1\nWORKBUDDY_ADAPTER_KEY=\n' > "$t1/.env"
adapter_test_fake_docker "$t1"
adapter_test_run "$repo_root" "$t1" docker.log WORKBUDDY_ADAPTER_KEY=''
grep -Fq -- 'profiles=workbuddy' "$t1/output-docker.log.log" || fail "workbuddy profile not selected"
adapter_test_assert_present "$t1/docker.log" "-f $t1/deploy/docker-compose.adapters.yml" "unified overlay not selected"
# 其他平台不得出现
adapter_test_assert_absent "$t1/docker.log" "sub2api-cursor" "cursor container started while disabled"
adapter_test_assert_absent "$t1/docker.log" "sub2api-desktop" "desktop container started while disabled"
# 密钥生成并持久化，且不出现在输出
key="$(awk -F= '$1=="WORKBUDDY_ADAPTER_KEY" && $2!="" {print $2}' "$t1/.env")"
[[ "$key" =~ ^[0-9a-f]{64}$ ]] || fail "workbuddy adapter key not generated"
grep -Fq -- "$key" "$t1/output-docker.log.log" && fail "adapter key leaked into output"
# 适配器早于候选
awk '/up -d --build --wait --wait-timeout 180/ {a=NR} /run -d --name sub2api-canary/ {c=NR} END{exit !(a>0 && c>a)}' "$t1/docker.log" \
  || fail "adapters were not started before the canary"

# ---- 场景 2：旧开关兼容 -----------------------------------------------------
t2="$(mktemp -d "${TMPDIR:-/tmp}/sub2api-adapter-legacy.XXXXXX")"
adapter_test_init "$repo_root" "$t2"
printf 'SUB2API_BUILTIN_ADAPTERS=1\n' > "$t2/.env"
adapter_test_fake_docker "$t2"
adapter_test_run "$repo_root" "$t2" docker.log
grep -Fq -- 'profiles=desktop,traework,workbuddy,vibex,zcode' "$t2/output-docker.log.log" \
  || fail "legacy switch did not map to the five local adapters"

# ---- 场景 3：多平台 + 外部服务 ---------------------------------------------
t3="$(mktemp -d "${TMPDIR:-/tmp}/sub2api-adapter-multi.XXXXXX")"
adapter_test_init "$repo_root" "$t3"
printf 'SUB2API_WORKBUDDY=1\nSUB2API_LAYA=1\nBUILTIN_ADAPTER_LAYA_URL=http://edge-laya:18082\n' > "$t3/.env"
adapter_test_fake_docker "$t3"
adapter_test_run "$repo_root" "$t3" docker.log
grep -Fq -- 'profiles=workbuddy,laya' "$t3/output-docker.log.log" || fail "multi-profile selection wrong"
# laya 是外部服务：只注入地址，不启动容器
adapter_test_assert_absent "$t3/docker.log" "sub2api-laya" "external service started a container"

# ---- 场景 4：外部服务未配置地址 => 视为关闭 ---------------------------------
t4="$(mktemp -d "${TMPDIR:-/tmp}/sub2api-adapter-extoff.XXXXXX")"
adapter_test_init "$repo_root" "$t4"
printf 'SUB2API_LAYA=1\nBUILTIN_ADAPTER_LAYA_URL=\nSUB2API_JEV=1\nBUILTIN_ADAPTER_JEV_URL=\n' > "$t4/.env"
adapter_test_fake_docker "$t4"
adapter_test_run "$repo_root" "$t4" docker.log
# 两个外部服务都未配置地址 => 都视为关闭 => 走 disabled 分支。
grep -Fq -- 'No built-in adapters enabled' "$t4/output-docker.log.log" || fail "unconfigured external services were enabled"

# ---- 场景 5：适配器健康失败 => 不切流 --------------------------------------
t5="$(mktemp -d "${TMPDIR:-/tmp}/sub2api-adapter-fail.XXXXXX")"
adapter_test_init "$repo_root" "$t5"
printf 'SUB2API_CURSOR=1\nCURSOR_ADAPTER_KEY=\n' > "$t5/.env"
adapter_test_fake_docker "$t5" "up -d --build --wait --wait-timeout 180"
if adapter_test_run "$repo_root" "$t5" docker-fail.log.log MOCK_FAIL=1; then
  fail "deployment succeeded despite adapter health failure"
fi
grep -Eq -- 'run -d --name sub2api-canary' "$t5/docker-fail.log.log" && fail "canary started after adapter failure"

# ---- 场景 6：全部关闭 => 不构建适配器 --------------------------------------
t6="$(mktemp -d "${TMPDIR:-/tmp}/sub2api-adapter-none.XXXXXX")"
adapter_test_init "$repo_root" "$t6"
printf 'SUB2API_REPLICAS=1\n' > "$t6/.env"
adapter_test_fake_docker "$t6"
adapter_test_run "$repo_root" "$t6" docker.log
grep -Fq -- 'No built-in adapters enabled' "$t6/output-docker.log.log" || fail "disabled path message missing"
adapter_test_assert_absent "$t6/docker.log" "up -d --build --wait" "adapters built while all disabled"
# 主服务重建也必须只针对 sub2api 本身
adapter_test_assert_present "$t6/docker.log" "sub2api" "primary service recreation missing"

for d in "$t1" "$t2" "$t3" "$t4" "$t5" "$t6"; do rm -rf "$d"; done
printf 'adapter registry deploy test passed\n'
