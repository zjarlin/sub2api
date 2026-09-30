#!/usr/bin/env bash
set -euo pipefail

repo_root="$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)"
test_dir="$(mktemp -d "${TMPDIR:-/tmp}/sub2api-single-node-deploy.XXXXXX")"
trap 'rm -rf "$test_dir"' EXIT
mkdir -p "$test_dir/deploy/cluster" "$test_dir/bin" "$test_dir/logs"
cp "$repo_root/deploy/cluster/deploy-252.sh" "$test_dir/deploy/cluster/deploy-252.sh"
cp "$repo_root/deploy/cluster/nginx.conf" "$test_dir/deploy/cluster/nginx.conf"
touch "$test_dir/deploy/docker-compose.yml" "$test_dir/deploy/cluster/docker-compose.yml" "$test_dir/docker-compose.override.yml"
: > "$test_dir/.env"

cat > "$test_dir/bin/docker" <<'SH'
#!/bin/sh
printf '%s\n' "$*" >> "$MOCK_DOCKER_LOG"

case "$*" in
  "ps --filter label=com.docker.compose.service=sub2api --format {{.Image}}")
    printf '%s\n' "old-image"
    ;;
  "ps --filter label=com.docker.compose.service=sub2api --format {{.Names}}")
    printf '%s\n' "sub2api-sub2api-1"
    ;;
  "inspect --format {{.Config.Image}} sub2api-sub2api-1")
    printf '%s\n' "old-image"
    ;;
  "inspect --format {{.State.Running}} sub2api-gateway")
    if [ "${MOCK_GATEWAY_RUNNING:-1}" = 1 ]; then printf 'true\n'; else exit 1; fi
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
  "ps --filter name=^sub2api-canary$ --filter status=running --format {{.ID}}")
    exit 0
    ;;
  "exec sub2api-gateway nginx -t"|"exec sub2api-gateway nginx -s reload")
    exit 0
    ;;
  *"run -d --no-deps --name sub2api-canary"*)
    if [ "${MOCK_CANARY_FAIL:-0}" = 1 ]; then printf 'canary failed\n'; exit 7; fi
    ;;
  "inspect --format {{(index (index .NetworkSettings.Ports \"8080/tcp\") 0).HostPort}} sub2api-canary")
    printf '18090\n'
    ;;
  "inspect --format {{(index (index .NetworkSettings.Ports \"8080/tcp\") 0).HostPort}} sub2api-canary")
    printf '18090\n'
    ;;
  "rm -sf sub2api-canary")
    exit 0
    ;;
  "logs "*)
    exit 0
    ;;
esac
SH

cat > "$test_dir/bin/curl" <<'SH'
#!/bin/sh
case " $* " in
  *"http://127.0.0.1:18090/ready"*)
    [ "${MOCK_CANARY_FAIL:-0}" = 1 ] && exit 22
    [ "${MOCK_CANARY_READY_FAIL:-0}" = 1 ] && exit 22
    exit 0
    ;;
  *"http://127.0.0.1:18080/ready"*)
    [ "${MOCK_GATEWAY_READY_FAIL:-0}" = 1 ] && exit 22
    exit 0
    ;;
esac
exit 0
SH
chmod +x "$test_dir/bin/docker" "$test_dir/bin/curl"

run_deploy() {
  MOCK_DOCKER_LOG="$1" MOCK_CANARY_FAIL="${MOCK_CANARY_FAIL:-0}" \
    MOCK_CANARY_READY_FAIL="${MOCK_CANARY_READY_FAIL:-0}" \
    MOCK_GATEWAY_READY_FAIL="${MOCK_GATEWAY_READY_FAIL:-0}" \
    DEPLOY_DIR="$test_dir" SUB2API_IMAGE=new-image CANARY_OBSERVE_SECONDS=0 CANARY_WAIT_SECONDS=1 \
    PATH="$test_dir/bin:$PATH" bash "$test_dir/deploy/cluster/deploy-252.sh"
}

run_deploy "$test_dir/success.log" > "$test_dir/success.out"
grep -Fq -- "run -d --no-deps --name sub2api-canary" "$test_dir/success.log"
grep -Fq -- "--publish 127.0.0.1::8080" "$test_dir/success.log"
grep -Fq -- "--label sub2api.role=release-candidate sub2api" "$test_dir/success.log"
if ! awk '
  /run -d --no-deps --name sub2api-canary/ { candidate=NR }
  /exec sub2api-gateway nginx -s reload/ { reload=NR }
  END { exit !(candidate > 0 && reload > candidate) }
' "$test_dir/success.log"; then
  printf 'gateway switched before candidate was started\n' >&2
  exit 1
fi
grep -Fq -- "up -d --wait --wait-timeout 1 --no-deps sub2api" "$test_dir/success.log"
grep -Fq -- "rm -sf sub2api-canary" "$test_dir/success.log"

if MOCK_CANARY_FAIL=1 run_deploy "$test_dir/canary-failed.log" > "$test_dir/canary-failed.out" 2>&1; then
  printf 'deployment succeeded although candidate startup failed\n' >&2
  exit 1
fi
if awk '
  /run -d --name sub2api-canary/ { candidate=NR }
  /exec sub2api-gateway nginx -s reload/ { reload=NR }
  END { exit !(candidate > 0 && reload > candidate) }
' "$test_dir/canary-failed.log"; then
  printf 'gateway switched after candidate startup failure\n' >&2
  exit 1
fi

if MOCK_GATEWAY_READY_FAIL=1 run_deploy "$test_dir/gateway-failed.log" > "$test_dir/gateway-failed.out" 2>&1; then
  printf 'deployment succeeded although gateway probe failed\n' >&2
  exit 1
fi
grep -Fq -- "http://sub2api:8080" "$test_dir/deploy/cluster/nginx.conf" || {
  printf 'gateway did not restore the primary after probe failure\n' >&2
  exit 1
}

printf 'single-node blue-green deploy tests passed\n'
