#!/usr/bin/env bash
set -euo pipefail

repo_root="$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)"
test_dir="$(mktemp -d "${TMPDIR:-/tmp}/sub2api-teleagent-deploy.XXXXXX")"
trap 'rm -rf "$test_dir"' EXIT

# 复用已有的隔离 Docker mock 验证集，不执行真实生产编排。
mkdir -p "$test_dir/deploy/tests" "$test_dir/deploy/cluster"
cp "$repo_root/deploy/cluster/deploy-252.sh" "$test_dir/deploy/cluster/deploy-252.sh"
cp "$repo_root/deploy/cluster/nginx.conf" "$test_dir/deploy/cluster/nginx.conf"
cp "$repo_root/deploy/docker-compose.teleagent.yml" "$test_dir/deploy/docker-compose.teleagent.yml"
sed -e 's/CURSOR/TELEAGENT/g' -e 's/Cursor/TeleAgent/g' -e 's/cursor/teleagent/g' \
  "$repo_root/deploy/tests/cursor-deploy-test.sh" > "$test_dir/deploy/tests/teleagent-generated-test.sh"
bash "$test_dir/deploy/tests/teleagent-generated-test.sh"
