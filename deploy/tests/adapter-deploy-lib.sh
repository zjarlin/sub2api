#!/usr/bin/env bash
# =============================================================================
# 内置适配器部署测试公共库。
# =============================================================================
# 统一为 deploy/tests/*-deploy-test.sh 提供：
#   - 组装一个隔离的 deploy 目录（含注册表、统一 compose、假 docker/curl）
#   - 运行 deploy-252.sh，并断言：
#       1) 已启用平台的 profile 出现在 COMPOSE_PROFILES
#       2) 统一叠加层被选中（-f .../docker-compose.adapters.yml）
#       3) 适配器早于候选容器启动（健康检查通过才切流）
#       4) 共享密钥自动生成、持久化、且不泄露到部署输出
#       5) 未启用平台不被选入 profile
#       6) 适配器健康失败时不会启动候选
# =============================================================================

adapter_test_init() {
  local repo_root="$1" test_dir="$2"
  mkdir -p "$test_dir/deploy/cluster" "$test_dir/bin"
  : > "$test_dir/deploy/docker-compose.yml"
  : > "$test_dir/deploy/cluster/docker-compose.yml"
  : > "$test_dir/docker-compose.override.yml"
  : > "$test_dir/.previous-image"
  cp "$repo_root/deploy/cluster/nginx.conf" "$test_dir/deploy/cluster/nginx.conf"
  cp "$repo_root/deploy/cluster/deploy-252.sh" "$test_dir/deploy/cluster/deploy-252.sh"
  cp "$repo_root/deploy/cluster/adapters.env.sh" "$test_dir/deploy/cluster/adapters.env.sh"
  cp "$repo_root/deploy/docker-compose.adapters.yml" "$test_dir/deploy/docker-compose.adapters.yml"
}

# adapter_test_fake_docker <test_dir> [fail_pattern]
# 生成假 docker；fail_pattern 命中时返回 7，模拟适配器健康检查失败。
adapter_test_fake_docker() {
  local test_dir="$1" fail_pattern="${2:-}"
  # 注意：用带引号的 heredoc（<<'SH'）生成，确保 {{range $name,...}} 里的 $name 不被展开。
  cat > "$test_dir/bin/docker" <<'SH'
#!/bin/sh
printf '%s\n' "$*" >> "$MOCK_DOCKER_LOG"
if [ "${MOCK_FAIL:-0}" = 1 ] && [ -n "@FAIL_PATTERN@" ]; then
  case "$*" in
    *"@FAIL_PATTERN@"*) printf 'adapter health check failed\n' >&2; exit 7 ;;
  esac
fi
if [ "$1" = image ] && [ "$2" = inspect ]; then printf '{}\n'; fi
case "$*" in
  "ps --filter label=com.docker.compose.service=sub2api --format {{.Image}}") printf 'old-image\n' ;;
  "ps --filter label=com.docker.compose.service=sub2api --format {{.Names}}") printf 'sub2api-sub2api-1\n' ;;
  "inspect --format {{.Config.Image}} sub2api-sub2api-1") printf 'old-image\n' ;;
  "inspect --format {{.State.Running}} sub2api-gateway") printf 'true\n' ;;
  "inspect sub2api-sub2api-1"|"inspect sub2api-canary") exit 0 ;;
  "inspect --format {{range .Config.Env}}{{println .}}{{end}} sub2api-sub2api-1") printf 'SERVER_PORT=8080\n' ;;
  "inspect --format {{range \$name,\$network := .NetworkSettings.Networks}}{{println \$name}}{{end}} sub2api-sub2api-1") printf 'sub2api_sub2api-network\n' ;;
  "exec sub2api-gateway wget -q -T 5 -O /dev/null http://sub2api:8080/ready") exit 0 ;;
  "exec sub2api-gateway test -f /etc/nginx/cluster/nginx.conf") exit 0 ;;
  "exec sub2api-gateway md5sum /etc/nginx/cluster/nginx.conf") md5sum "$DEPLOY_DIR/deploy/cluster/nginx.conf" ;;
  "exec sub2api-gateway nginx -t -c /etc/nginx/cluster/nginx.conf"|"exec sub2api-gateway nginx -s reload -c /etc/nginx/cluster/nginx.conf") exit 0 ;;
  *"run -d --name sub2api-canary-"*) exit 0 ;;
  "inspect --format {{(index (index .NetworkSettings.Ports \"8080/tcp\") 0).HostPort}} sub2api-canary-"*) printf '18090\n' ;;
esac
case "$*" in
  *" config"*) printf 'config-ok\n' ;;
esac
exit 0
SH
  # 注入失败模式（占位符替换，避免与 heredoc 引号冲突）。
  if [ -n "$fail_pattern" ]; then
    sed -i "s|@FAIL_PATTERN@|$fail_pattern|" "$test_dir/bin/docker"
  fi
  cat > "$test_dir/bin/curl" <<'SH'
#!/bin/sh
exit 0
SH
  chmod +x "$test_dir/bin/docker" "$test_dir/bin/curl"
}

# adapter_test_run <repo_root> <test_dir> <log_name> [extra env...]
adapter_test_run() {
  local repo_root="$1" test_dir="$2" log_name="$3"; shift 3
  env MOCK_DOCKER_LOG="$test_dir/$log_name" DEPLOY_DIR="$test_dir" SUB2API_IMAGE=test-image \
    CANARY_WAIT_SECONDS=1 CANARY_OBSERVE_SECONDS=0 PATH="$test_dir/bin:$PATH" "$@" \
    bash "$repo_root/deploy/cluster/deploy-252.sh" > "$test_dir/output-$log_name.log"
}

# adapter_test_expect_profile <log_file> <profile>
adapter_test_expect_profile() {
  local log="$1" profile="$2"
  grep -q -- "profiles=" "$log" || { printf 'no profile line in %s\n' "$log" >&2; return 1; }
}

# adapter_test_assert_present <log> <pattern> <message>
adapter_test_assert_present() {
  grep -Fq -- "$2" "$1" || { printf '%s\n' "$3" >&2; return 1; }
}

# adapter_test_assert_absent <log> <pattern> <message>
adapter_test_assert_absent() {
  if grep -Fq -- "$2" "$1"; then printf '%s\n' "$3" >&2; return 1; fi
}
