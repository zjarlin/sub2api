#!/usr/bin/env bash
#
# deploy/macmini/macmini-watch.sh 的行为测试：用临时 git 仓库 + 假 deploy 脚本验证
# 「跟随远端 main 部署」「同一提交不重复部署」「工作区脏时不强改」「部署失败可重试」。
#
# 只在本地/CI 使用，不连接任何真实主机，也不调用 docker。
set -euo pipefail

repo_root="$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)"
watch_src="$repo_root/deploy/macmini/macmini-watch.sh"
test_dir="$(mktemp -d "${TMPDIR:-/tmp}/sub2api-macmini-watch.XXXXXX")"
trap 'rm -rf "$test_dir"' EXIT

fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
pass() { printf 'ok - %s\n' "$*"; }

git_q() { git -c user.email=test@example.com -c user.name=test -c commit.gpgsign=false "$@"; }

# 远端仓库：一个 bare 仓库充当 origin。
git init --quiet --bare "$test_dir/remote.git"
git clone --quiet "$test_dir/remote.git" "$test_dir/seed" 2>/dev/null
mkdir -p "$test_dir/seed/deploy/macmini"
cp "$watch_src" "$test_dir/seed/deploy/macmini/macmini-watch.sh"
# 部署脚本用假实现：记录被调用的提交，并可按环境变量模拟失败。
cat > "$test_dir/seed/deploy/macmini/deploy-macmini.sh" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "${EXPECTED_GIT_SHA:-none}" >> "$FAKE_DEPLOY_LOG"
[ "${EXPECTED_GIT_SHA:-none}" = "${SUB2API_GIT_SHA:-none}" ] || { echo "sha mismatch" >&2; exit 9; }
actual_head="$(git -C "$REPO_DIR" rev-parse HEAD)"
[ "$actual_head" = "${EXPECTED_GIT_SHA:-none}" ] || { echo "repo head != expected" >&2; exit 9; }
[ "${FAKE_DEPLOY_FAIL:-0}" = 1 ] && { echo "simulated failure" >&2; exit 5; }
exit 0
SH
chmod +x "$test_dir/seed/deploy/macmini/deploy-macmini.sh"
printf 'v1\n' > "$test_dir/seed/app.txt"
# 状态文件落在工作副本里，必须被忽略，否则它自己会把工作区弄脏。
printf '.macmini-watch.deployed\n' > "$test_dir/seed/.gitignore"
git_q -C "$test_dir/seed" add -A
git_q -C "$test_dir/seed" commit --quiet -m "init"
git_q -C "$test_dir/seed" push --quiet origin HEAD:refs/heads/main

# 工作副本：模拟 Mac mini 上的 ~/sub2api-deploy-src。
git clone --quiet --branch main "$test_dir/remote.git" "$test_dir/work"
work="$test_dir/work"
export FAKE_DEPLOY_LOG="$test_dir/deploy.log"
: > "$FAKE_DEPLOY_LOG"

# 假 docker 只是满足脚本的 docker 存在性检查。
mkdir -p "$test_dir/bin"
cat > "$test_dir/bin/docker" <<'SH'
#!/usr/bin/env bash
exit 0
SH
chmod +x "$test_dir/bin/docker"

run_watch() {
  # 用一个能读到「部署脚本所在目录 HEAD」的包装层：部署脚本里拿不到 sha 时由这里注入。
  PATH="$test_dir/bin:/usr/bin:/bin" \
  REPO_DIR="$work" \
  WATCH_ONCE=1 \
  FAKE_DEPLOY_LOG="$FAKE_DEPLOY_LOG" \
  bash -c '
    set -euo pipefail
    export FAKE_DEPLOY_LOG="$FAKE_DEPLOY_LOG"
    exec bash "'"$work"'/deploy/macmini/macmini-watch.sh"
  '
}

# 1) 首次运行：部署远端 main 的提交并写状态文件。
head1="$(git -C "$work" rev-parse HEAD)"
run_watch
grep -qx "$head1" "$FAKE_DEPLOY_LOG" || fail "expected deploy of $head1, log: $(cat "$FAKE_DEPLOY_LOG")"
[ -f "$work/.macmini-watch.deployed" ] || fail "state file not written"
pass "first run deploys remote main"

# 2) 同一提交再跑一次：不重复部署。
run_watch
[ "$(grep -c . "$FAKE_DEPLOY_LOG")" = "1" ] || fail "duplicate deploy on unchanged remote"
pass "unchanged remote does not redeploy"

# 3) 远端前进一个提交：工作副本被快进并部署新提交。
printf 'v2\n' > "$test_dir/seed/app.txt"
git_q -C "$test_dir/seed" commit --quiet -am "v2"
git_q -C "$test_dir/seed" push --quiet origin HEAD:refs/heads/main
head2="$(git -C "$test_dir/seed" rev-parse HEAD)"
run_watch
[ "$(git -C "$work" rev-parse HEAD)" = "$head2" ] || fail "worktree not updated to $head2"
grep -qx "$head2" "$FAKE_DEPLOY_LOG" || fail "expected deploy of $head2"
pass "new remote commit is deployed"

# 4) 工作副本有本地未提交改动且远端又前进：只跳过，不改工作区。
printf 'local-only\n' > "$work/uncommitted.txt"
printf 'v3\n' > "$test_dir/seed/app.txt"
git_q -C "$test_dir/seed" commit --quiet -am "v3"
git_q -C "$test_dir/seed" push --quiet origin HEAD:refs/heads/main
head3="$(git -C "$test_dir/seed" rev-parse HEAD)"
run_watch
[ "$(git -C "$work" rev-parse HEAD)" = "$head2" ] || fail "dirty worktree was rewritten"
[ -f "$work/uncommitted.txt" ] || fail "local file was lost"
! grep -qx "$head3" "$FAKE_DEPLOY_LOG" || fail "deployed over dirty worktree"
pass "dirty worktree is skipped, not rewritten"
rm -f "$work/uncommitted.txt"

# 5) 部署失败：不写状态文件，下次轮询会重试。
FAKE_DEPLOY_FAIL=1 run_watch || true
[ "$(awk '{print $1}' "$work/.macmini-watch.deployed")" = "$head2" ] || fail "state advanced on failed deploy"
run_watch
grep -qx "$head3" "$FAKE_DEPLOY_LOG" || fail "expected retry deploy of $head3"
[ "$(awk '{print $1}' "$work/.macmini-watch.deployed")" = "$head3" ] || fail "state not advanced after successful retry"
pass "failed deploy is retried on next tick"

# 6) 缺少 REPO_DIR：必须报错退出，不能静默回退到当前目录。
if PATH="$test_dir/bin:/usr/bin:/bin" WATCH_ONCE=1 bash "$watch_src" 2>/dev/null; then
  fail "missing REPO_DIR should fail"
fi
pass "missing REPO_DIR fails fast"

printf 'macmini watch tests passed\n'
