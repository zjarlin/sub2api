#!/usr/bin/env bash
#
# Mac mini 跟随部署守护：轮询 origin/main，发现新提交就用 deploy-macmini.sh 部署。
#
# 用途：GitHub Actions 在本仓库（fork）上被禁用时，流水线仍要能「推 main 即部署」。
# 这里只做「取最新提交 + 调同一个部署脚本」，构建/备份/候选预检/切流/回滚
# 全部复用 deploy-macmini.sh，不维护第二套部署实现。
#
# 所有主机/目录/间隔都来自变量（launchd plist 里集中定义）：
#   REPO_DIR          源码工作副本（必填，无默认值）
#   WATCH_INTERVAL    轮询间隔秒（默认 120）
#   WATCH_BRANCH      跟随的分支（默认 main）
#   WATCH_REMOTE      远端名（默认 origin）
#   WATCH_STATE       「已部署提交」记录文件（默认 $REPO_DIR/.macmini-watch.deployed）
#   其余变量（DEPLOY_DIR / DATA_DIR / SUB2API_*）原样透传给 deploy-macmini.sh。
#
# 安全边界：
#   - 工作副本 HEAD 与远端不一致且有本地未提交改动时，只记录并跳过，不强改工作区。
#   - 同一个提交部署成功后写入状态文件，重复轮询不会重复部署。
#   - 部署脚本自身失败时按失败处理：不写状态文件，下次轮询重试并留下日志。
set -euo pipefail

log() { printf '[macmini-watch] %s %s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" "$*"; }

: "${REPO_DIR:?REPO_DIR is required}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WATCH_INTERVAL="${WATCH_INTERVAL:-120}"
WATCH_BRANCH="${WATCH_BRANCH:-main}"
WATCH_REMOTE="${WATCH_REMOTE:-origin}"
WATCH_STATE="${WATCH_STATE:-$REPO_DIR/.macmini-watch.deployed}"
DEPLOY_SCRIPT="$SCRIPT_DIR/deploy-macmini.sh"

[ -d "$REPO_DIR/.git" ] || { log "REPO_DIR is not a git checkout: $REPO_DIR"; exit 1; }
[ -x "$DEPLOY_SCRIPT" ] || { log "missing deploy script: $DEPLOY_SCRIPT"; exit 1; }
command -v git >/dev/null || { log "git not found"; exit 1; }
command -v docker >/dev/null || { log "docker not found"; exit 1; }

remote_sha() {
  git -C "$REPO_DIR" ls-remote "$WATCH_REMOTE" "refs/heads/$WATCH_BRANCH" | awk '{print $1}' | head -1
}

last_deployed() { [ -f "$WATCH_STATE" ] && awk '{print $1}' "$WATCH_STATE" || true; }

# 状态文件落在工作副本里，必须先被 .gitignore 覆盖，否则它自己会把工作区弄脏。
ensure_state_ignored() {
  local rel
  rel="$(basename "$WATCH_STATE")"
  case "$WATCH_STATE" in
    "$REPO_DIR"/*) ;;
    *) return 0 ;;
  esac
  local dir part
  dir="$(dirname "${WATCH_STATE#"$REPO_DIR"/}")"
  part="$rel"
  [ "$dir" = "." ] || part="$dir/$rel"
  if ! git -C "$REPO_DIR" check-ignore -q "$part" 2>/dev/null; then
    log "WATCH_STATE is inside the worktree but not ignored: $part"
    return 1
  fi
}

# 只把「除状态文件之外」的改动当脏：状态文件由本脚本管理。
worktree_dirty() {
  local ignored=""
  case "$WATCH_STATE" in
    "$REPO_DIR"/*) ignored=":(exclude)${WATCH_STATE#"$REPO_DIR"/}" ;;
  esac
  if [ -n "$(git -C "$REPO_DIR" status --porcelain -- . "$ignored" 2>/dev/null | head -1)" ]; then
    return 0
  fi
  [ -n "$(git -C "$REPO_DIR" status --porcelain --untracked-files=all -- . "$ignored" 2>/dev/null | head -1)" ]
}

record_deployed() {
  local sha="$1"
  if worktree_dirty; then
    log "refusing to record state: worktree under $REPO_DIR is dirty (contains local edits)"
    return 1
  fi
  printf '%s %s\n' "$sha" "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" > "$WATCH_STATE"
}

deploy_once() {
  local sha="$1"
  log "deploying $sha (previous: $(last_deployed || true))"
  # 部署脚本按 REPO_DIR 的 HEAD 构建，并用 EXPECTED_GIT_SHA 自己再校验一次，
  # 保证「构建的代码」与「远端 main 的提交」是同一个。
  # 用 export 而不是「VAR=... cmd」前缀：后者只作用于 bash 进程本身，
  # 不会传给部署脚本内部的 docker build 等子进程。
  export PATH="/usr/local/bin:/opt/homebrew/bin:${PATH:-}"
  export REPO_DIR="$REPO_DIR"
  export SUB2API_GIT_SHA="$sha"
  export EXPECTED_GIT_SHA="$sha"
  export SUB2API_BUILD="${SUB2API_BUILD:-1}"
  export DEPLOY_DIR="${DEPLOY_DIR:-$HOME/DockerCompose/dockge/stacks/sub2api}"
  export DATA_DIR="${DATA_DIR:-$HOME/DockerCompose/sub2api/data}"
  bash "$DEPLOY_SCRIPT"
}

tick() {
  local remote local_head deployed
  remote="$(remote_sha || true)"
  [ -n "$remote" ] || { log "cannot resolve $WATCH_REMOTE/$WATCH_BRANCH (network?); will retry"; return 0; }
  local_head="$(git -C "$REPO_DIR" rev-parse HEAD 2>/dev/null || true)"
  deployed="$(last_deployed)"

  [ "$remote" = "$deployed" ] && return 0

  if [ "$local_head" != "$remote" ]; then
    if worktree_dirty; then
      log "skip $remote: $REPO_DIR has local changes, refusing to rewrite worktree"
      return 0
    fi
    log "fetching $WATCH_REMOTE/$WATCH_BRANCH"
    git -C "$REPO_DIR" fetch --quiet "$WATCH_REMOTE" "refs/heads/$WATCH_BRANCH" || {
      log "fetch failed; will retry"; return 0; }
    git -C "$REPO_DIR" checkout --quiet -B "$WATCH_BRANCH" FETCH_HEAD || {
      log "checkout failed; will retry"; return 0; }
    local_head="$(git -C "$REPO_DIR" rev-parse HEAD)"
  fi

  [ "$local_head" = "$remote" ] || { log "HEAD $local_head != remote $remote; skip"; return 0; }

  if deploy_once "$remote"; then
    record_deployed "$remote" && log "deployed $remote"
  else
    log "deploy of $remote FAILED; will retry next tick"
  fi
}

ensure_state_ignored || log "warning: state file may dirty the worktree; deployments will be skipped while it is dirty"

if [ "${WATCH_ONCE:-0}" = "1" ]; then
  tick
  exit 0
fi

log "watching $WATCH_REMOTE/$WATCH_BRANCH every ${WATCH_INTERVAL}s (repo=$REPO_DIR)"
while :; do
  tick || log "tick failed unexpectedly"
  sleep "$WATCH_INTERVAL"
done
