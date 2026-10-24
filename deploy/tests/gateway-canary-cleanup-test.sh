#!/usr/bin/env bash
#
# deploy/gateway/canary-cleanup.sh 的回归测试（只用本地临时文件，不调用真实 docker）。
#
# 覆盖 2026-10-24 的真实缺陷：cleanup 末尾用 `[ -n "$mount_work" ] && rm -rf ...`，
# mount_work 为空时返回 1，部署脚本在 `set -e` 下于「switching live container」
# 之前静默退出（#267 就是这样失败：候选已健康、线上未切换、也没有回滚日志）。
set -euo pipefail

repo_root="$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)"

# 用函数覆盖 docker，避免测试触碰真实容器。
docker_calls="$repo_root/deploy/tests/.canary-cleanup-docker-calls.$$"
rm -f "$docker_calls"
docker() { printf '%s\n' "$*" >> "$docker_calls"; }

# shellcheck source=../gateway/canary-cleanup.sh
. "$repo_root/deploy/gateway/canary-cleanup.sh"

test_dir="$(mktemp -d "${TMPDIR:-/tmp}/sub2api-canary-cleanup-test.XXXXXX")"
trap 'rm -rf "$test_dir" "$docker_calls"' EXIT

fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
pass() { printf 'ok - %s\n' "$*"; }

# 1) 空 mount_work：必须返回 0，否则 set -e 会中断整个部署脚本。
if ! cleanup_canary "sub2api-canary-empty" ""; then
  fail "cleanup_canary returned non-zero with empty work dir (set -e would abort the deploy)"
fi
pass "empty work dir returns zero"
grep -q '^rm -f sub2api-canary-empty$' "$docker_calls" \
  || fail "canary container was not removed: $(cat "$docker_calls" 2>/dev/null)"
pass "canary container removed"

# 2) 已有临时目录：目录要被删掉，且仍返回 0。
work_dir="$test_dir/mount-plan"
mkdir -p "$work_dir"
printf '[]\n' > "$work_dir/mounts.json"
cleanup_canary "sub2api-canary-work" "$work_dir" \
  || fail "cleanup_canary returned non-zero with work dir"
[ -e "$work_dir" ] && fail "work dir was not removed: $work_dir"
pass "work dir removed"

# 3) 直接模拟 deploy-gateway.sh 的调用形态：先清空 mount_work，再调用 cleanup。
#    这正是 #267 失败的那一步。
mount_work="$test_dir/second"
mkdir -p "$mount_work"
cleanup_canary "sub2api-canary-trap" "$mount_work"
mount_work=""
cleanup_canary "sub2api-canary-trap" "$mount_work" \
  || fail "second cleanup (empty mount_work) failed; deploy would abort before switching"
pass "cleanup stays fatal-free after mount_work is cleared"

printf '\ngateway canary cleanup tests passed\n'
