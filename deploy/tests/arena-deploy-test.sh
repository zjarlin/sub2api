#!/usr/bin/env bash
set -euo pipefail

repo_root="$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)"
test_dir="$(mktemp -d "${TMPDIR:-/tmp}/sub2api-arena-deploy.XXXXXX")"
trap 'rm -rf "$test_dir"' EXIT
mkdir -p "$test_dir/deploy/cluster" "$test_dir/bin"
touch "$test_dir/deploy/docker-compose.yml" "$test_dir/deploy/cluster/docker-compose.yml" "$test_dir/docker-compose.override.yml"
cp "$repo_root/deploy/docker-compose.arena.yml" "$test_dir/deploy/docker-compose.arena.yml"
printf 'SUB2API_ARENA=1\nARENA_AGENT_BRIDGE_KEY=\n' > "$test_dir/.env"

cat > "$test_dir/bin/docker" <<'SH'
#!/bin/sh
printf '%s\n' "$*" >> "$MOCK_DOCKER_LOG"
if [ "${ARENA_AGENT_BRIDGE_KEY+x}" = x ] && [ -z "$ARENA_AGENT_BRIDGE_KEY" ]; then
  printf 'empty exported key overrides .env\n' >&2
  exit 1
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
if rg -Fq -- "$key" "$test_dir/output.log"; then
  printf 'Arena key appeared in deployment output\n' >&2
  exit 1
fi
MOCK_DOCKER_LOG="$test_dir/docker-second.log" DEPLOY_DIR="$test_dir" SUB2API_IMAGE=test-image \
  PATH="$test_dir/bin:$PATH" bash "$repo_root/deploy/cluster/deploy-252.sh" > /dev/null
persisted_key="$(awk -F= '$1 == "ARENA_AGENT_BRIDGE_KEY" && $2 != "" { key=$2 } END { print key }' "$test_dir/.env")"
test "$key" = "$persisted_key"

MOCK_DOCKER_LOG="$test_dir/docker-disabled.log" DEPLOY_DIR="$test_dir" SUB2API_IMAGE=test-image \
  SUB2API_ARENA=0 PATH="$test_dir/bin:$PATH" bash "$repo_root/deploy/cluster/deploy-252.sh" > /dev/null
if rg -Fq -- 'docker-compose.arena.yml' "$test_dir/docker-disabled.log" || rg -Fq -- 'up -d --build sub2api-arena' "$test_dir/docker-disabled.log"; then
  printf 'Disabled Arena adapter was selected\n' >&2
  exit 1
fi
printf 'Arena deploy wiring and key persistence test passed\n'
