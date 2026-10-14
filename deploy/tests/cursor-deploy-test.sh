#!/usr/bin/env bash
set -euo pipefail

unset SUB2API_CURSOR CURSOR_ADAPTER_KEY MOCK_CURSOR_FAIL

repo_root="$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)"
test_dir="$(mktemp -d "${TMPDIR:-/tmp}/sub2api-cursor-deploy.XXXXXX")"
trap 'rm -rf "$test_dir"' EXIT
mkdir -p "$test_dir/deploy/cluster" "$test_dir/bin"
touch "$test_dir/deploy/docker-compose.yml" "$test_dir/deploy/cluster/docker-compose.yml" "$test_dir/docker-compose.override.yml"
cp "$repo_root/deploy/cluster/nginx.conf" "$test_dir/deploy/cluster/nginx.conf"
cp "$repo_root/deploy/docker-compose.cursor.yml" "$test_dir/deploy/docker-compose.cursor.yml"
printf 'SUB2API_CURSOR=1\nCURSOR_ADAPTER_KEY=\n' > "$test_dir/.env"

cat > "$test_dir/bin/docker" <<'SH'
#!/bin/sh
printf '%s\n' "$*" >> "$MOCK_DOCKER_LOG"
if [ "${CURSOR_ADAPTER_KEY+x}" = x ] && [ -z "$CURSOR_ADAPTER_KEY" ]; then
  printf 'empty exported key overrides .env\n' >&2
  exit 1
fi
if [ "${MOCK_CURSOR_FAIL:-0}" = 1 ]; then
  case "$*" in
    *' up -d --build --wait --wait-timeout 180 sub2api-cursor')
      printf 'Cursor adapter failed health check\n' >&2
      exit 7
      ;;
  esac
fi
if [ "$1" = image ] && [ "$2" = inspect ]; then
  printf '{}\n'
fi
case "$*" in
  "ps --filter label=com.docker.compose.service=sub2api --format {{.Image}}")
    printf 'old-image\n'
    ;;
  "ps --filter label=com.docker.compose.service=sub2api --format {{.Names}}")
    printf 'sub2api-sub2api-1\n'
    ;;
  "inspect --format {{.Config.Image}} sub2api-sub2api-1")
    printf 'old-image\n'
    ;;
  "inspect --format {{.State.Running}} sub2api-gateway")
    printf 'true\n'
    ;;
  "inspect sub2api-sub2api-1"|"inspect sub2api-canary")
    exit 0
    ;;
  "inspect --format {{range .Config.Env}}{{println .}}{{end}} sub2api-sub2api-1")
    printf 'SERVER_PORT=8080\n'
    ;;
  "inspect --format {{range \$name,\$network := .NetworkSettings.Networks}}{{println \$name}}{{end}} sub2api-sub2api-1")
    printf 'sub2api_sub2api-network\n'
    ;;
  "exec sub2api-gateway wget -q -T 5 -O /dev/null http://sub2api:8080/ready")
    exit 0
    ;;
  "exec sub2api-gateway test -f /etc/nginx/cluster/nginx.conf")
    exit 0
    ;;
  "exec sub2api-gateway md5sum /etc/nginx/cluster/nginx.conf")
    md5sum "$DEPLOY_DIR/deploy/cluster/nginx.conf"
    ;;
  "exec sub2api-gateway nginx -t -c /etc/nginx/cluster/nginx.conf"|"exec sub2api-gateway nginx -s reload -c /etc/nginx/cluster/nginx.conf")
    exit 0
    ;;
  *"run -d --name sub2api-canary-"*)
    exit 0
    ;;
  "inspect --format {{(index (index .NetworkSettings.Ports \"8080/tcp\") 0).HostPort}} sub2api-canary-"*)
    printf '18090\n'
    ;;
esac
SH
cat > "$test_dir/bin/curl" <<'SH'
#!/bin/sh
exit 0
SH
chmod +x "$test_dir/bin/docker" "$test_dir/bin/curl"

MOCK_DOCKER_LOG="$test_dir/docker.log" DEPLOY_DIR="$test_dir" SUB2API_IMAGE=test-image CANARY_WAIT_SECONDS=1 CANARY_OBSERVE_SECONDS=0 \
  CURSOR_ADAPTER_KEY='' PATH="$test_dir/bin:$PATH" \
  bash "$repo_root/deploy/cluster/deploy-252.sh" > "$test_dir/output.log"

key="$(awk -F= '$1 == "CURSOR_ADAPTER_KEY" && $2 != "" { key=$2 } END { print key }' "$test_dir/.env")"
if [[ ! "$key" =~ ^[0-9a-f]{64}$ ]]; then
  printf 'Cursor adapter key was not generated\n' >&2
  exit 1
fi
if ! grep -Fq -- "-f $test_dir/deploy/docker-compose.cursor.yml" "$test_dir/docker.log"; then
  printf 'Cursor compose overlay was not selected\n' >&2
  exit 1
fi
if ! awk '
  / up -d --build --wait --wait-timeout 180 sub2api-cursor$/ { adapter_line=NR }
  /run -d --name sub2api-canary/ { canary_line=NR }
  END { exit !(adapter_line > 0 && canary_line > adapter_line) }
' "$test_dir/docker.log"; then
  printf 'Cursor adapter was not healthy before the canary\n' >&2
  exit 1
fi
if grep -Fq -- "$key" "$test_dir/output.log"; then
  printf 'Cursor adapter key appeared in deployment output\n' >&2
  exit 1
fi

MOCK_DOCKER_LOG="$test_dir/docker-second.log" DEPLOY_DIR="$test_dir" SUB2API_IMAGE=test-image CANARY_WAIT_SECONDS=1 CANARY_OBSERVE_SECONDS=0 \
  PATH="$test_dir/bin:$PATH" bash "$repo_root/deploy/cluster/deploy-252.sh" > /dev/null
persisted_key="$(awk -F= '$1 == "CURSOR_ADAPTER_KEY" && $2 != "" { key=$2 } END { print key }' "$test_dir/.env")"
test "$key" = "$persisted_key"

MOCK_DOCKER_LOG="$test_dir/docker-disabled.log" DEPLOY_DIR="$test_dir" SUB2API_IMAGE=test-image CANARY_WAIT_SECONDS=1 CANARY_OBSERVE_SECONDS=0 \
  SUB2API_CURSOR=0 PATH="$test_dir/bin:$PATH" \
  bash "$repo_root/deploy/cluster/deploy-252.sh" > /dev/null
if grep -Fq -- 'docker-compose.cursor.yml' "$test_dir/docker-disabled.log" ||
  grep -Fq -- 'up -d --build --wait --wait-timeout 180 sub2api-cursor' "$test_dir/docker-disabled.log"; then
  printf 'Disabled Cursor adapter was selected\n' >&2
  exit 1
fi

if MOCK_DOCKER_LOG="$test_dir/docker-failed.log" DEPLOY_DIR="$test_dir" SUB2API_IMAGE=test-image CANARY_WAIT_SECONDS=1 CANARY_OBSERVE_SECONDS=0 \
  MOCK_CURSOR_FAIL=1 PATH="$test_dir/bin:$PATH" \
  bash "$repo_root/deploy/cluster/deploy-252.sh" > "$test_dir/failed-output.log" 2>&1; then
  printf 'Deployment succeeded despite Cursor adapter health failure\n' >&2
  exit 1
fi
if grep -Eq -- 'run -d --name sub2api-canary' "$test_dir/docker-failed.log"; then
  printf 'Canary started after Cursor adapter health failure\n' >&2
  exit 1
fi

printf 'Cursor deploy wiring and key persistence tests passed\n'
