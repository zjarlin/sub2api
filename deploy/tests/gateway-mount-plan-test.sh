#!/usr/bin/env bash
#
# deploy/gateway/mount-plan.sh 的回归测试（只用本地临时文件，不调用 docker）。
#
# 覆盖 2026-10-24 的真实缺陷：bind 挂载没有 Name 字段，若按 tab 分隔逐字段
# read，连续分隔符会被折叠、字段错位，整条 bind 挂载被静默丢掉。那会让候选
# 容器「健康检查通过、切流后丢 /app/data」。
set -euo pipefail

repo_root="$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)"
# shellcheck source=../gateway/mount-plan.sh
. "$repo_root/deploy/gateway/mount-plan.sh"

test_dir="$(mktemp -d "${TMPDIR:-/tmp}/sub2api-mount-plan-test.XXXXXX")"
trap 'rm -rf "$test_dir"' EXIT

fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
pass() { printf 'ok - %s\n' "$*"; }

write_mount_plan "$repo_root/deploy/tests/fixtures/mounts-live.json" \
  "$test_dir/specs" "$test_dir/dests" 2>"$test_dir/stderr" \
  || fail "write_mount_plan returned non-zero"

# bind 必须出现在结果里：这是本次修复的核心。
expected_specs='/root/DockerCompose/sub2api/data:/app/data'
actual_specs="$(cat "$test_dir/specs")"
[ "$actual_specs" = "$expected_specs" ] \
  || fail "bind spec lost: expected [$expected_specs] got [$actual_specs]"
pass "bind mount survives parsing"

expected_dests='/app/data'
actual_dests="$(cat "$test_dir/dests")"
[ "$actual_dests" = "$expected_dests" ] \
  || fail "destination mismatch: expected [$expected_dests] got [$actual_dests]"
pass "destination list matches"

# named volume 也要能复现（源用卷名，不是宿主路径）。
cat > "$test_dir/mounts-volume.json" <<'JSON'
[{"Type":"volume","Name":"legacy_data","Source":"/var/lib/docker/volumes/legacy_data/_data","Destination":"/app/legacy"}]
JSON
write_mount_plan "$test_dir/mounts-volume.json" "$test_dir/vspecs" "$test_dir/vdests" 2>/dev/null \
  || fail "volume plan failed"
[ "$(cat "$test_dir/vspecs")" = 'legacy_data:/app/legacy' ] \
  || fail "volume spec wrong: $(cat "$test_dir/vspecs")"
pass "named volume replicated by name"

# 无法用 -v 复现的类型要跳过而不是产出坏参数。
cat > "$test_dir/mounts-mixed.json" <<'JSON'
[{"Type":"tmpfs","Name":"","Source":"","Destination":"/app/tmp"},
 {"Type":"bind","Name":"","Source":"/srv/data","Destination":"/app/data"}]
JSON
write_mount_plan "$test_dir/mounts-mixed.json" "$test_dir/mspecs" "$test_dir/mdests" 2>"$test_dir/merr" \
  || fail "mixed plan failed"
[ "$(cat "$test_dir/mspecs")" = '/srv/data:/app/data' ] \
  || fail "unexpected specs: $(cat "$test_dir/mspecs")"
grep -q 'skip tmpfs mount at /app/tmp' "$test_dir/merr" \
  || fail "tmpfs skip was not reported"
pass "unsupported mount types are skipped loudly"

# 空挂载列表必须产出空结果而不是报错（历史 Exited 容器可能没有挂载）。
printf '[]\n' > "$test_dir/mounts-empty.json"
write_mount_plan "$test_dir/mounts-empty.json" "$test_dir/especs" "$test_dir/edests" 2>/dev/null \
  || fail "empty plan failed"
[ -s "$test_dir/especs" ] && fail "expected empty specs"
[ -s "$test_dir/edests" ] && fail "expected empty dests"
pass "empty mount list yields empty plan"

printf '\ngateway mount plan tests passed\n'
