#!/usr/bin/env bash
set -euo pipefail

repo_root="$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)"
test_dir="$(mktemp -d "${TMPDIR:-/tmp}/sub2api-deepseek-deploy.XXXXXX")"
trap 'rm -rf "$test_dir"' EXIT
mkdir -p "$test_dir/deploy/cluster" "$test_dir/bin"
touch "$test_dir/deploy/docker-compose.yml" "$test_dir/deploy/cluster/docker-compose.yml" "$test_dir/docker-compose.override.yml"
cp "$repo_root/deploy/docker-compose.deepseek-web.yml" "$test_dir/deploy/docker-compose.deepseek-web.yml"
printf 'SUB2API_DEEPSEEK_WEB=1\nDEEPSEEK_WEB_ADAPTER_KEY=\n' > "$test_dir/.env"

cat > "$test_dir/bin/docker" <<'SH'
#!/bin/sh
printf '%s\n' "$*" >> "$MOCK_DOCKER_LOG"
if [ "$1" = compose ] || [ "$1" = --project-name ]; then
  if [ "${DEEPSEEK_WEB_ADAPTER_KEY+x}" = x ] && [ -z "$DEEPSEEK_WEB_ADAPTER_KEY" ]; then
    printf 'empty exported key overrides .env\n' >&2
    exit 1
  fi
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
  DEEPSEEK_WEB_ADAPTER_KEY='' PATH="$test_dir/bin:$PATH" \
  bash "$repo_root/deploy/cluster/deploy-252.sh" > /dev/null

key="$(awk -F= '$1 == "DEEPSEEK_WEB_ADAPTER_KEY" && $2 != "" { key=$2 } END { print key }' "$test_dir/.env")"
if [[ ! "$key" =~ ^[0-9a-f]{64}$ ]]; then
  printf 'DeepSeek adapter key was not generated\n' >&2
  exit 1
fi
if ! grep -Fq -- "-f $test_dir/deploy/docker-compose.deepseek-web.yml" "$test_dir/docker.log"; then
  printf 'DeepSeek compose overlay was not selected\n' >&2
  exit 1
fi
if ! grep -Fq -- 'up -d --build sub2api-deepseek-web' "$test_dir/docker.log"; then
  printf 'DeepSeek adapter was not started\n' >&2
  exit 1
fi
if grep -Fq -- 'up -d --build sub2api-desktop' "$test_dir/docker.log"; then
  printf 'Unrelated built-in adapters were started\n' >&2
  exit 1
fi
printf 'DeepSeek web deploy wiring test passed\n'
