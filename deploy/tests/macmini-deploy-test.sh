#!/usr/bin/env bash
#
# deploy/macmini/deploy-macmini.sh 的行为测试：用假的 docker/curl 驱动完整流程，
# 覆盖切流/回滚、隔离预演、预演库守卫和 named volume 拒绝等路径。
#
# 只在本地/CI 使用，不连接任何真实主机。
set -euo pipefail

repo_root="$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)"
test_dir="$(mktemp -d "${TMPDIR:-/tmp}/sub2api-macmini-deploy.XXXXXX")"
trap 'rm -rf "$test_dir"' EXIT

mkdir -p "$test_dir/bin" "$test_dir/deploy" "$test_dir/data" "$test_dir/live-data" "$test_dir/canary-data" "$test_dir/backups" "$test_dir/logs"
: > "$test_dir/deploy/docker-compose.yml"

cat > "$test_dir/bin/docker" <<'SH'
#!/usr/bin/env bash
# 记录所有调用；状态用 $MOCK_STATE 目录里的文件模拟，不接触真实 docker。
set -euo pipefail
ORIGINAL_PATH="$PATH"
# macOS 没有 GNU dirname 时会回落到桌面版 docker，必须锁定假命令目录。
PATH="/opt/cloud-dev/bin:/usr/local/bin:/usr/bin:/bin:${PATH:-}"
export PATH
printf '%s\n' "$*" >> "$MOCK_DOCKER_LOG"

image_name() {
  local spec="$1"
  case "$spec" in
    *":latest") printf '%s\n' "$spec" ;;
    *) printf '%s\n' "$spec" ;;
  esac
}

command_args=("$@")
case "${command_args[*]}" in
  "inspect sub2api")
    PATH="$ORIGINAL_PATH"
    exit 0
    ;;
  'inspect -f {{range .Mounts}}{{if eq .Destination "/app/data"}}{{.Source}}{{end}}{{end}} sub2api')
    PATH="$ORIGINAL_PATH"
    printf '%s\n' "$MOCK_DATA_DIR"
    exit 0
    ;;
  'inspect -f {{.Image}} sub2api')
    PATH="$ORIGINAL_PATH"
    printf 'sha256:previous\n'
    exit 0
    ;;
  'inspect -f {{.Config.Image}} sub2api')
    PATH="$ORIGINAL_PATH"
    printf 'weishaw/sub2api:latest\n'
    exit 0
    ;;
  'inspect -f {{json .Mounts}} sub2api')
    PATH="$ORIGINAL_PATH"
    if [ "${MOCK_NAMED_VOLUME:-0}" = 1 ]; then
      printf '[{"Type":"bind","Source":"%s","Destination":"/app/data"},{"Type":"volume","Name":"legacy-vol","Destination":"/app/legacy"}]\n' "$MOCK_DATA_DIR"
    else
      printf '[{"Type":"bind","Source":"%s","Destination":"/app/data"}]\n' "$MOCK_DATA_DIR"
    fi
    exit 0
    ;;
  "inspect pgvector")
    exit 0
    ;;
  'inspect -f {{range .Mounts}}{{if eq .Destination "/app/data"}}{{.Source}}{{end}}{{end}} sub2api')
    printf '%s\n' "$MOCK_DATA_DIR"
    ;;
  'inspect -f {{.Image}} sub2api')
    printf '%s\n' "sha256:previous"
    ;;
  'inspect -f {{.Config.Image}} sub2api')
    printf '%s\n' "weishaw/sub2api:latest"
    ;;
  'inspect -f {{range $k, $v := .NetworkSettings.Networks}}{{$k}}{{end}} sub2api')
    printf '%s\n' "sub2api_default"
    ;;
  'inspect -f {{range .Config.Env}}{{println .}}{{end}} sub2api')
    printf 'SERVER_PORT=18080\nJWT_SECRET=test\n'
    ;;
  'inspect -f {{json .Mounts}} sub2api')
    if [ "${MOCK_NAMED_VOLUME:-0}" = 1 ]; then
      printf '[{"Type":"bind","Source":"%s","Destination":"/app/data"},{"Type":"volume","Name":"legacy-vol","Destination":"/app/legacy"}]\n' "$MOCK_DATA_DIR"
    else
      printf '[{"Type":"bind","Source":"%s","Destination":"/app/data"}]\n' "$MOCK_DATA_DIR"
    fi
    ;;
  'inspect -f {{.State.Status}} sub2api-canary-'*)
    printf 'running\n'
    ;;
  'inspect -f {{.State.Status}} sub2api')
    printf 'running\n'
    ;;
  "image inspect "*)
    exit 0
    ;;
  "buildx build "*)
    printf '%s\n' "$*" >> "$MOCK_BUILD_LOG"
    ;;
  run\ -d\ --name\ sub2api-canary-*)
    if [ "${MOCK_CANARY_FAIL:-0}" = 1 ]; then
      printf 'candidate failed to start\n' >&2
      exit 7
    fi
    ;;
  "rm -f sub2api-canary-"*)
    ;;
  "rm -f sub2api")
    ;;
  compose\ *)
    printf '%s\n' "$*" >> "$MOCK_COMPOSE_LOG"
    ;;
  "logs --tail "*)
    printf 'mock logs\n'
    ;;
  'ps --filter name=^sub2api$ --filter health=healthy -q')
    if [ "${MOCK_LIVE_UNHEALTHY:-0}" = 1 ]; then
      exit 0
    fi
    printf 'abc\n'
    ;;
  'ps --filter name=^sub2api$ --format {{.Names}} {{.Image}} {{.Status}}')
    printf 'sub2api %s rolled-back\n' "weishaw/sub2api:latest"
    ;;
  'exec -i pgvector pg_dump -U postgres -d sub2api -Fc')
    printf 'PGDMPmock-database-backup\n'
    ;;
  'exec pgvector psql -U postgres -d sub2api -Atc SELECT count(*) FROM accounts; SELECT count(*) FROM account_groups;')
    calls=0
    [ ! -f "$MOCK_STATE/baseline_calls" ] || calls="$(cat "$MOCK_STATE/baseline_calls")"
    calls=$((calls + 1))
    printf '%s\n' "$calls" > "$MOCK_STATE/baseline_calls"
    if [ "$calls" -ge 2 ] && [ "${MOCK_DATA_CHANGED:-0}" = 1 ]; then
      printf '9\n4\n'
    elif [ "${MOCK_BAD_BASELINE:-0}" = 1 ]; then
      printf 'ERROR: relation accounts does not exist\n'
    else
      printf '10\n5\n'
    fi
    ;;
  exec\ -i\ pgvector\ pg_restore\ -l)
    printf '1; 0 0 TABLE public accounts postgres\n'
    ;;
  *)
    printf 'unexpected docker call: %s\n' "$*" >&2
    exit 99
    ;;
esac
SH

cat > "$test_dir/bin/curl" <<'SH'
#!/usr/bin/env bash
# 按阶段返回健康结果：候选端口恒为成功，宿主 18080 由 MOCK_LIVE_UNHEALTHY 控制。
set -euo pipefail
printf '%s\n' "$*" >> "$MOCK_CURL_LOG"
url="${*: -1}"
case "$url" in
  *":18080/health")
    [ "${MOCK_LIVE_UNHEALTHY:-0}" = 1 ] && exit 22
    exit 0
    ;;
  *"/health")
    exit 0
    ;;
  *)
    exit 1
    ;;
esac
SH

chmod +x "$test_dir/bin/docker" "$test_dir/bin/curl"

run_case() {
  local name="$1"
  local expect_rc="$2"
  shift 2
  local case_dir="$test_dir/$name"
  local live_dir="$case_dir/data"
  mkdir -p "$case_dir/deploy" "$case_dir/data" "$case_dir/backups" "$case_dir/logs" "$live_dir" "$case_dir/state"
  : > "$case_dir/deploy/docker-compose.yml"

  set +e
  env \
    PATH="$test_dir/bin:$PATH" \
    REPO_DIR="$repo_root" \
    DEPLOY_DIR="$case_dir/deploy" \
    DATA_DIR="$case_dir/data" \
    BACKUP_DIR="$case_dir/backups" \
    MOCK_DATA_DIR="$live_dir" \
    MOCK_DOCKER_LOG="$case_dir/logs/docker.log" \
    MOCK_CURL_LOG="$case_dir/logs/curl.log" \
    MOCK_BUILD_LOG="$case_dir/logs/build.log" \
    MOCK_COMPOSE_LOG="$case_dir/logs/compose.log" \
    MOCK_STATE="$case_dir/state" \
    SUB2API_GIT_SHA="0123456789abcdef0123456789abcdef01234567" \
    SUB2API_BUILD=1 \
    CANARY_PORT=19001 \
    CANARY_WAIT_SECONDS=2 LIVE_WAIT_SECONDS=2 \
    "$@" \
    bash "$repo_root/deploy/macmini/deploy-macmini.sh" > "$case_dir/logs/stdout.log" 2>&1
  local rc=$?
  set -e

  if [ "$expect_rc" = 0 ] && [ "$rc" -ne 0 ]; then
    printf '%s: expected success, got rc=%s\n' "$name" "$rc" >&2
    cat "$case_dir/logs/stdout.log" >&2
    return 1
  fi
  if [ "$expect_rc" = 1 ] && [ "$rc" -eq 0 ]; then
    printf '%s: expected failure, got success\n' "$name" >&2
    cat "$case_dir/logs/stdout.log" >&2
    printf 'docker log:\n' >&2
    cat "$case_dir/logs/docker.log" >&2 || true
    return 1
  fi
}

# 场景 1：候选健康、宿主健康 → 切流并写部署标记。
run_case success 0
grep -q 'up -d --no-deps sub2api' "$test_dir/success/logs/compose.log"
test -f "$test_dir/success/deploy/DEPLOYED_IMAGE"
test -f "$test_dir/success/deploy/DEPLOYED_RELEASE"
grep -q 'zjarlin/sub2api:0123456789ab' "$test_dir/success/deploy/DEPLOYED_IMAGE"
test -f "$test_dir/success/deploy/docker-compose.deploy.yml"
grep -q 'image: zjarlin/sub2api:0123456789ab' "$test_dir/success/deploy/docker-compose.deploy.yml"
test -s "$test_dir/success/backups/sub2api-before-deploy-"*.dump
grep -qF -- "-v $test_dir/success/data:/app/data" "$test_dir/success/logs/docker.log"
grep -q 'account data unchanged by candidate migration: 10 accounts, 5 account-group links' "$test_dir/success/logs/stdout.log"
grep -qF 'exec pgvector psql -U postgres -d sub2api -Atc' "$test_dir/success/logs/docker.log"

# 场景 2：候选起不来 → 绝对不允许执行 compose 切流。
run_case canary_failure 1 MOCK_CANARY_FAIL=1
test ! -s "$test_dir/canary_failure/logs/compose.log"
test -s "$test_dir/canary_failure/backups/sub2api-before-deploy-"*.dump
backup_line="$(grep -n 'exec -i pgvector pg_dump' "$test_dir/canary_failure/logs/docker.log" | cut -d: -f1)"
candidate_line="$(grep -n 'run -d --name sub2api-canary-' "$test_dir/canary_failure/logs/docker.log" | cut -d: -f1)"
test "$backup_line" -lt "$candidate_line"

# 场景 3：切流后宿主健康检查失败 → 必须回滚且不能写成功标记。
run_case rollback 1 MOCK_LIVE_UNHEALTHY=1
grep -q 'up -d --no-deps sub2api' "$test_dir/rollback/logs/compose.log"
grep -q 'rm -f sub2api' "$test_dir/rollback/logs/docker.log"
test ! -f "$test_dir/rollback/deploy/DEPLOYED_IMAGE"

# 场景 3b：候选迁移后账号数量减少 → 不切流；库查询失败或结果格式错误同样拒绝。
run_case account_data_changed 1 MOCK_DATA_CHANGED=1
test ! -s "$test_dir/account_data_changed/logs/compose.log"
test ! -f "$test_dir/account_data_changed/deploy/DEPLOYED_IMAGE"
grep -q 'account data changed during candidate migration' "$test_dir/account_data_changed/logs/stdout.log"
run_case account_data_unreadable 1 MOCK_BAD_BASELINE=1
test ! -s "$test_dir/account_data_unreadable/logs/compose.log"
grep -q 'invalid account data baseline' "$test_dir/account_data_unreadable/logs/stdout.log"

# 场景 4：隔离预演（DATA_DIR_MUST_MATCH=0）→ 候选被强制连预演库，且绝不切流。
mkdir -p "$test_dir/live-data" "$test_dir/canary-data"
run_case canary_isolated 0 MOCK_DATA_DIR="$test_dir/live-data" DATA_DIR="$test_dir/canary-data" DATA_DIR_MUST_MATCH=0 CANARY_ONLY=1 CANARY_DATABASE_NAME=sub2api_canary_test
test ! -s "$test_dir/canary_isolated/logs/compose.log"
test ! -f "$test_dir/canary_isolated/deploy/DEPLOYED_IMAGE"
grep -q 'DATABASE_DBNAME=sub2api_canary_test' "$test_dir/canary_isolated/logs/docker.log"
grep -qF -- "-v $test_dir/canary-data:/app/data" "$test_dir/canary_isolated/logs/docker.log"
# 隔离预演不允许把现役目录带进候选。
! grep -qF -- "$test_dir/live-data:/app/data" "$test_dir/canary_isolated/logs/docker.log"
! grep -q 'psql -U postgres -d sub2api -Atc' "$test_dir/canary_isolated/logs/docker.log"

# 场景 5：隔离预演必须显式给出预演库名，否则直接失败。
run_case canary_isolated_guard 1 DATA_DIR_MUST_MATCH=0 CANARY_ONLY=1 CANARY_DATABASE_NAME=
if grep -q "run -d --name sub2api-canary-" "$test_dir/canary_isolated_guard/logs/docker.log"; then
  printf 'canary_isolated_guard: candidate started without a canary database\n' >&2
  exit 1
fi

# 场景 6：现役容器带 named volume → 直接失败，不允许把 named volume 复制进候选。
run_case named_volume_guard 1 MOCK_NAMED_VOLUME=1
test ! -s "$test_dir/named_volume_guard/logs/compose.log"
if grep -q "run -d --name sub2api-canary-" "$test_dir/named_volume_guard/logs/docker.log"; then
  printf 'named_volume_guard: candidate started despite a named volume\n' >&2
  exit 1
fi

printf 'macmini deploy tests passed\n'
