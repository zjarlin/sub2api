#!/usr/bin/env bash
set -euo pipefail

unset SUB2API_WINDSURF WINDSURF_ADAPTER_KEY MOCK_WINDSURF_FAIL

repo_root="$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)"
test_dir="$(mktemp -d "${TMPDIR:-/tmp}/sub2api-windsurf-deploy.XXXXXX")"
trap 'rm -rf "$test_dir"' EXIT
mkdir -p "$test_dir/deploy/cluster" "$test_dir/bin"
touch "$test_dir/deploy/docker-compose.yml" "$test_dir/deploy/cluster/docker-compose.yml" "$test_dir/docker-compose.override.yml"
cp "$repo_root/deploy/cluster/nginx.conf" "$test_dir/deploy/cluster/nginx.conf"
cp "$repo_root/deploy/docker-compose.windsurf.yml" "$test_dir/deploy/docker-compose.windsurf.yml"
printf 'SUB2API_WINDSURF=1\nWINDSURF_ADAPTER_KEY=\n' > "$test_dir/.env"

cat > "$test_dir/bin/docker" <<'SH'
#!/bin/sh
printf '%s\n' "$*" >> "$MOCK_DOCKER_LOG"
if [ "${WINDSURF_ADAPTER_KEY+x}" = x ] && [ -z "$WINDSURF_ADAPTER_KEY" ]; then
  printf 'empty exported key overrides .env\n' >&2
  exit 1
fi
if [ "${MOCK_WINDSURF_FAIL:-0}" = 1 ]; then
  case "$*" in
    *' up -d --build --wait --wait-timeout 180 sub2api-windsurf')
      printf 'Windsurf adapter failed health check\n' >&2
      exit 7
      ;;
  esac
fi
if [ "$1" = image ] && [ "$2" = inspect ]; then
  printf '{}\n'
fi
SH
cat > "$test_dir/bin/curl" <<'SH'
#!/bin/sh
exit 0
SH
chmod +x "$test_dir/bin/docker" "$test_dir/bin/curl"

MOCK_DOCKER_LOG="$test_dir/docker.log" DEPLOY_DIR="$test_dir" SUB2API_IMAGE=test-image CANARY_WAIT_SECONDS=1 CANARY_OBSERVE_SECONDS=0 \
  WINDSURF_ADAPTER_KEY='' PATH="$test_dir/bin:$PATH" \
  bash "$repo_root/deploy/cluster/deploy-252.sh" > "$test_dir/output.log"

key="$(awk -F= '$1 == "WINDSURF_ADAPTER_KEY" && $2 != "" { key=$2 } END { print key }' "$test_dir/.env")"
if [[ ! "$key" =~ ^[0-9a-f]{64}$ ]]; then
  printf 'Windsurf adapter key was not generated\n' >&2
  exit 1
fi
if ! grep -Fq -- "-f $test_dir/deploy/docker-compose.windsurf.yml" "$test_dir/docker.log"; then
  printf 'Windsurf compose overlay was not selected\n' >&2
  exit 1
fi
if ! awk '
  / up -d --build --wait --wait-timeout 180 sub2api-windsurf$/ { adapter_line=NR }
  / run -d --no-deps --name sub2api-canary/ { canary_line=NR }
  END { exit !(adapter_line > 0 && canary_line > adapter_line) }
' "$test_dir/docker.log"; then
  printf 'Windsurf adapter was not healthy before the canary\n' >&2
  exit 1
fi
if grep -Fq -- "$key" "$test_dir/output.log"; then
  printf 'Windsurf adapter key appeared in deployment output\n' >&2
  exit 1
fi

MOCK_DOCKER_LOG="$test_dir/docker-second.log" DEPLOY_DIR="$test_dir" SUB2API_IMAGE=test-image CANARY_WAIT_SECONDS=1 CANARY_OBSERVE_SECONDS=0 \
  PATH="$test_dir/bin:$PATH" bash "$repo_root/deploy/cluster/deploy-252.sh" > /dev/null
persisted_key="$(awk -F= '$1 == "WINDSURF_ADAPTER_KEY" && $2 != "" { key=$2 } END { print key }' "$test_dir/.env")"
test "$key" = "$persisted_key"

MOCK_DOCKER_LOG="$test_dir/docker-disabled.log" DEPLOY_DIR="$test_dir" SUB2API_IMAGE=test-image CANARY_WAIT_SECONDS=1 CANARY_OBSERVE_SECONDS=0 \
  SUB2API_WINDSURF=0 PATH="$test_dir/bin:$PATH" \
  bash "$repo_root/deploy/cluster/deploy-252.sh" > /dev/null
if grep -Fq -- 'docker-compose.windsurf.yml' "$test_dir/docker-disabled.log" ||
  grep -Fq -- 'up -d --build --wait --wait-timeout 180 sub2api-windsurf' "$test_dir/docker-disabled.log"; then
  printf 'Disabled Windsurf adapter was selected\n' >&2
  exit 1
fi

if MOCK_DOCKER_LOG="$test_dir/docker-failed.log" DEPLOY_DIR="$test_dir" SUB2API_IMAGE=test-image CANARY_WAIT_SECONDS=1 CANARY_OBSERVE_SECONDS=0 \
  MOCK_WINDSURF_FAIL=1 PATH="$test_dir/bin:$PATH" \
  bash "$repo_root/deploy/cluster/deploy-252.sh" > "$test_dir/failed-output.log" 2>&1; then
  printf 'Deployment succeeded despite Windsurf adapter health failure\n' >&2
  exit 1
fi
if grep -Fq -- 'run -d --no-deps --name sub2api-canary' "$test_dir/docker-failed.log"; then
  printf 'Canary started after Windsurf adapter health failure\n' >&2
  exit 1
fi

printf 'Windsurf deploy wiring and key persistence tests passed\n'
