#!/usr/bin/env bash
set -euo pipefail

# 各场景自行设置开关，避免调用测试时的环境影响默认行为和测试密钥。
unset SUB2API_ARENA SUB2API_ARENA_PROXY ARENA_AGENT_BRIDGE_KEY MOCK_ARENA_PROXY_FAIL

repo_root="$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)"
test_dir="$(mktemp -d "${TMPDIR:-/tmp}/sub2api-arena-deploy.XXXXXX")"
trap 'rm -rf "$test_dir"' EXIT
mkdir -p "$test_dir/deploy/cluster" "$test_dir/bin"
touch "$test_dir/deploy/docker-compose.yml" "$test_dir/deploy/cluster/docker-compose.yml" "$test_dir/docker-compose.override.yml"
cp "$repo_root/deploy/docker-compose.arena.yml" "$test_dir/deploy/docker-compose.arena.yml"
cp "$repo_root/deploy/docker-compose.arena-proxy.yml" "$test_dir/deploy/docker-compose.arena-proxy.yml"
printf 'SUB2API_ARENA=1\nARENA_AGENT_BRIDGE_KEY=\n' > "$test_dir/.env"

cat > "$test_dir/bin/docker" <<'SH'
#!/bin/sh
printf '%s\n' "$*" >> "$MOCK_DOCKER_LOG"
if [ "${ARENA_AGENT_BRIDGE_KEY+x}" = x ] && [ -z "$ARENA_AGENT_BRIDGE_KEY" ]; then
  printf 'empty exported key overrides .env\n' >&2
  exit 1
fi
if [ "${MOCK_ARENA_PROXY_FAIL:-0}" = 1 ]; then
  case "$*" in
    *' up -d --wait --wait-timeout 180 sub2api-arena-proxy')
      printf 'Arena proxy failed health check\n' >&2
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

assert_proxy_disabled() {
  local docker_log="$1"
  if rg -Fq -- 'docker-compose.arena-proxy.yml' "$docker_log" || \
    rg -Fq -- 'up -d --wait --wait-timeout 180 sub2api-arena-proxy' "$docker_log"; then
    printf 'Disabled Arena proxy was selected or started\n' >&2
    exit 1
  fi
}

assert_proxy_enabled() {
  local docker_log="$1"
  if ! rg -Fq -- "-f $test_dir/deploy/docker-compose.arena-proxy.yml" "$docker_log"; then
    printf 'Arena proxy compose overlay was not selected\n' >&2
    exit 1
  fi
  if ! awk '
    / up -d --wait --wait-timeout 180 sub2api-arena-proxy$/ { proxy_line=NR }
    / up -d --build sub2api-arena$/ { arena_line=NR }
    END { exit !(proxy_line > 0 && arena_line > proxy_line) }
  ' "$docker_log"; then
    printf 'Arena proxy was not started with a health wait before Arena\n' >&2
    exit 1
  fi
}

MOCK_DOCKER_LOG="$test_dir/docker.log" DEPLOY_DIR="$test_dir" SUB2API_IMAGE=test-image \
  ARENA_AGENT_BRIDGE_KEY='' PATH="$test_dir/bin:$PATH" \
  bash "$repo_root/deploy/cluster/deploy-252.sh" > "$test_dir/output.log"

key="$(awk -F= '$1 == "ARENA_AGENT_BRIDGE_KEY" && $2 != "" { key=$2 } END { print key }' "$test_dir/.env")"
if [[ ! "$key" =~ ^[0-9a-f]{64}$ ]]; then
  printf 'Arena adapter key was not generated\n' >&2
  exit 1
fi
if ! rg -Fq -- "-f $test_dir/deploy/docker-compose.arena.yml --profile arena" "$test_dir/docker.log"; then
  printf 'Arena compose overlay or profile was not selected\n' >&2
  exit 1
fi
if ! rg -Fq -- 'up -d --build sub2api-arena' "$test_dir/docker.log"; then
  printf 'Arena adapter was not started\n' >&2
  exit 1
fi
assert_proxy_disabled "$test_dir/docker.log"
if rg -Fq -- "$key" "$test_dir/output.log"; then
  printf 'Arena key appeared in deployment output\n' >&2
  exit 1
fi
MOCK_DOCKER_LOG="$test_dir/docker-second.log" DEPLOY_DIR="$test_dir" SUB2API_IMAGE=test-image \
  PATH="$test_dir/bin:$PATH" bash "$repo_root/deploy/cluster/deploy-252.sh" > /dev/null
persisted_key="$(awk -F= '$1 == "ARENA_AGENT_BRIDGE_KEY" && $2 != "" { key=$2 } END { print key }' "$test_dir/.env")"
test "$key" = "$persisted_key"

MOCK_DOCKER_LOG="$test_dir/docker-proxy-env.log" DEPLOY_DIR="$test_dir" SUB2API_IMAGE=test-image \
  SUB2API_ARENA_PROXY=1 PATH="$test_dir/bin:$PATH" \
  bash "$repo_root/deploy/cluster/deploy-252.sh" > /dev/null
assert_proxy_enabled "$test_dir/docker-proxy-env.log"

printf '\nSUB2API_ARENA_PROXY=1\n' >> "$test_dir/.env"
MOCK_DOCKER_LOG="$test_dir/docker-proxy-dotenv.log" DEPLOY_DIR="$test_dir" SUB2API_IMAGE=test-image \
  PATH="$test_dir/bin:$PATH" bash "$repo_root/deploy/cluster/deploy-252.sh" > /dev/null
assert_proxy_enabled "$test_dir/docker-proxy-dotenv.log"

MOCK_DOCKER_LOG="$test_dir/docker-proxy-disabled.log" DEPLOY_DIR="$test_dir" SUB2API_IMAGE=test-image \
  SUB2API_ARENA_PROXY=0 PATH="$test_dir/bin:$PATH" \
  bash "$repo_root/deploy/cluster/deploy-252.sh" > /dev/null
assert_proxy_disabled "$test_dir/docker-proxy-disabled.log"
if ! rg -Fq -- 'up -d --build sub2api-arena' "$test_dir/docker-proxy-disabled.log"; then
  printf 'Disabling the Arena proxy also disabled Arena\n' >&2
  exit 1
fi

# 只有精确的 1 才启用代理，其他显式值不能回退到 .env 的 1。
MOCK_DOCKER_LOG="$test_dir/docker-proxy-other-value.log" DEPLOY_DIR="$test_dir" SUB2API_IMAGE=test-image \
  SUB2API_ARENA_PROXY=2 PATH="$test_dir/bin:$PATH" \
  bash "$repo_root/deploy/cluster/deploy-252.sh" > /dev/null
assert_proxy_disabled "$test_dir/docker-proxy-other-value.log"

MOCK_DOCKER_LOG="$test_dir/docker-disabled.log" DEPLOY_DIR="$test_dir" SUB2API_IMAGE=test-image \
  SUB2API_ARENA=0 SUB2API_ARENA_PROXY=1 PATH="$test_dir/bin:$PATH" \
  bash "$repo_root/deploy/cluster/deploy-252.sh" > /dev/null
if rg -Fq -- 'docker-compose.arena.yml' "$test_dir/docker-disabled.log" || rg -Fq -- 'up -d --build sub2api-arena' "$test_dir/docker-disabled.log"; then
  printf 'Disabled Arena adapter was selected\n' >&2
  exit 1
fi
assert_proxy_disabled "$test_dir/docker-disabled.log"

if MOCK_DOCKER_LOG="$test_dir/docker-proxy-failed.log" DEPLOY_DIR="$test_dir" SUB2API_IMAGE=test-image \
  SUB2API_ARENA_PROXY=1 MOCK_ARENA_PROXY_FAIL=1 PATH="$test_dir/bin:$PATH" \
  bash "$repo_root/deploy/cluster/deploy-252.sh" > "$test_dir/proxy-failed-output.log" 2>&1; then
  printf 'Deployment succeeded despite the Arena proxy health failure\n' >&2
  exit 1
fi
if ! rg -Fq -- 'up -d --wait --wait-timeout 180 sub2api-arena-proxy' "$test_dir/docker-proxy-failed.log"; then
  printf 'Arena proxy health failure was not exercised\n' >&2
  exit 1
fi
if rg -Fq -- 'up -d --build sub2api-arena' "$test_dir/docker-proxy-failed.log"; then
  printf 'Arena was started after the proxy health failure\n' >&2
  exit 1
fi
printf 'Arena deploy wiring, proxy startup and key persistence tests passed\n'
