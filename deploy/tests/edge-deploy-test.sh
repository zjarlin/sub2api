#!/usr/bin/env bash
set -euo pipefail

repo_root="$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)"
test_dir="$(mktemp -d "${TMPDIR:-/tmp}/sub2api-edge-deploy.XXXXXX")"
trap 'rm -rf "$test_dir"' EXIT
fixture="$test_dir/deployment dir"
mkdir -p "$test_dir/bin" "$test_dir/images" "$fixture"
export MOCK_IMAGE_DIR="$test_dir/images" MOCK_DOCKER_LOG="$test_dir/docker.log" MOCK_COMPOSE_LOG="$test_dir/compose.log"
unset EDGE_VISION_IMAGE EDGE_MEDIA_IMAGE EDGE_MAIL_IMAGE EDGE_LAYA_IMAGE EDGE_FORCE_BUILD
unset EDGE_VISION_IMAGE_REPOSITORY EDGE_MEDIA_IMAGE_REPOSITORY EDGE_MAIL_IMAGE_REPOSITORY EDGE_LAYA_IMAGE_REPOSITORY
unset EDGE_MAIL_PYTHON_IMAGE EDGE_LAYA_PYTHON_IMAGE EDGE_VISION_DIR EDGE_MEDIA_DIR EDGE_MAIL_DIR
unset EDGE_LAYA_MODELS_DIR MOCK_FAIL_BUILD

# 替身只记录部署行为，不接触生产 Docker；镜像缓存跨多次脚本调用保留。
cat > "$test_dir/bin/docker" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "$MOCK_DOCKER_LOG"
case "$1 $2" in
  'network inspect') exit 0 ;;
  'image inspect')
    key="$(printf '%s' "$3" | sha256sum | cut -d ' ' -f 1)"
    test -f "$MOCK_IMAGE_DIR/$key"
    exit
    ;;
esac
case "$1" in
  build)
    if [ "${MOCK_FAIL_BUILD:-0}" = 1 ]; then exit 7; fi
    shift
    while [ "$#" -gt 0 ]; do
      if [ "$1" = -t ]; then
        key="$(printf '%s' "$2" | sha256sum | cut -d ' ' -f 1)"
        touch "$MOCK_IMAGE_DIR/$key"
        shift
      fi
      shift
    done
    ;;
  compose)
    [[ "$*" != *--force-recreate* && "$*" != *--no-recreate* && "$*" == *--no-build* ]]
    service="$(basename "$(dirname "$(dirname "$3")")")"
    image="${EDGE_VISION_IMAGE:-${EDGE_MEDIA_IMAGE:-${EDGE_MAIL_IMAGE:-${EDGE_LAYA_IMAGE:-}}}}"
    printf '%s\t%s\t%s\n' "$service" "$image" "${EDGE_MEDIA_WORKERS:-1}" >> "$MOCK_COMPOSE_LOG"
    ;;
  inspect) printf 'healthy\n' ;;
  *) printf '未预期的 Docker 调用: %s\n' "$*" >&2; exit 1 ;;
esac
SH
chmod +x "$test_dir/bin/docker"

services=(edge-vision edge-media edge-mail edge-laya)
for service in "${services[@]}"; do
  directory="$fixture/$service"
  mkdir -p "$directory/docker" "$directory/app" "$directory/compose" "$directory/models"
  printf 'FROM test-base\nCOPY app /app\n' > "$directory/docker/Dockerfile"
  printf 'print("initial")\n' > "$directory/app/main.py"
  printf '__pycache__/\n' > "$directory/.dockerignore"
  cp "$repo_root/$service/compose/docker-compose.yml" "$directory/compose/docker-compose.yml"
done
for service in edge-vision edge-media edge-mail; do
  printf 'dependency==1\n' > "$fixture/$service/requirements.txt"
done
printf 'model-v1\n' > "$fixture/edge-vision/models/model.onnx"
mkdir -p "$fixture/edge-laya/models/checkpoint/multilingual"
for prefix in checkpoint checkpoint/multilingual; do
  printf 'weights\n' > "$fixture/edge-laya/models/$prefix/model.safetensors"
  printf '{}\n' > "$fixture/edge-laya/models/$prefix/rl_agent_config.json"
done

run_deploy() {
  local service="$1"
  shift
  local script="deploy-$service.sh"
  if [ "$service" = edge-laya ]; then script=deploy-laya.sh; fi
  env DEPLOY_DIR="$fixture" PATH="$test_dir/bin:$PATH" "$@" \
    bash "$repo_root/deploy/cluster/$script" > "$test_dir/output.log" 2>&1
}
run_all() {
  local service
  for service in "${services[@]}"; do run_deploy "$service" "$@"; done
}
reset_logs() {
  : > "$MOCK_DOCKER_LOG"
  : > "$MOCK_COMPOSE_LOG"
}
assert_builds() {
  local actual
  actual="$(awk '$1 == "build" { count++ } END { print count+0 }' "$MOCK_DOCKER_LOG")"
  if [ "$actual" != "$1" ]; then
    cat "$MOCK_DOCKER_LOG" >&2
    printf '预期构建 %s 次，实际 %s 次\n' "$1" "$actual" >&2
    exit 1
  fi
}

reset_logs
run_all
assert_builds 4
cp "$MOCK_COMPOSE_LOG" "$test_dir/initial-images"

# 新仓库提交与重新解压产生的时间、属主变化不应改变独立服务镜像。
printf 'unrelated backend change\n' > "$fixture/backend.txt"
find "$fixture" -type f -exec touch -t 202001010000 {} +
if [ "$(id -u)" = 0 ]; then
  chown 1001:1001 "$fixture/edge-mail/app/main.py"
fi
reset_logs
run_all
assert_builds 0
cmp "$MOCK_COMPOSE_LOG" "$test_dir/initial-images"

# 运行参数、Compose 和仅挂载的模型变化允许更新容器，但不能触发镜像构建。
printf '\n# 配置变更\n' >> "$fixture/edge-media/compose/docker-compose.yml"
printf 'new weights\n' > "$fixture/edge-laya/models/checkpoint/model.safetensors"
reset_logs
run_all EDGE_MEDIA_WORKERS=2
assert_builds 0
awk -F '\t' '$1 == "edge-media" { if ($3 != 2) exit 1; found=1 } END { if (!found) exit 1 }' "$MOCK_COMPOSE_LOG"

# 应用修改只构建对应服务，其他三个服务保持原镜像引用。
printf 'print("updated")\n' > "$fixture/edge-mail/app/main.py"
reset_logs
run_all
assert_builds 1
awk -F '\t' '$1 != "edge-mail"' "$test_dir/initial-images" > "$test_dir/unchanged-before"
awk -F '\t' '$1 != "edge-mail"' "$MOCK_COMPOSE_LOG" > "$test_dir/unchanged-after"
cmp "$test_dir/unchanged-before" "$test_dir/unchanged-after"
test "$(awk -F '\t' '$1 == "edge-mail" { print $2 }' "$test_dir/initial-images")" != \
  "$(awk -F '\t' '$1 == "edge-mail" { print $2 }' "$MOCK_COMPOSE_LOG")"

# 依赖、随镜像打包的视觉模型和构建参数都必须使缓存失效。
printf 'dependency==2\n' > "$fixture/edge-mail/requirements.txt"
reset_logs
run_all
assert_builds 1
printf 'model-v2\n' > "$fixture/edge-vision/models/model.onnx"
reset_logs
run_all
assert_builds 1
reset_logs
run_all EDGE_MAIL_PYTHON_IMAGE=python:new-base
assert_builds 1
reset_logs
run_all EDGE_MAIL_PYTHON_IMAGE=python:new-base
assert_builds 0

# 显式不可变镜像版本可复用，强制构建仅在运维明确要求时生效。
reset_logs
run_deploy edge-mail EDGE_MAIL_IMAGE=example/mail:release
assert_builds 1
reset_logs
run_deploy edge-mail EDGE_MAIL_IMAGE=example/mail:release
assert_builds 0
reset_logs
run_deploy edge-mail EDGE_MAIL_IMAGE=example/mail:release EDGE_FORCE_BUILD=1
assert_builds 1

# 构建失败不能继续启动服务，也不能将失败记录当作镜像缓存。
printf 'print("build failure")\n' > "$fixture/edge-mail/app/main.py"
reset_logs
if run_deploy edge-mail MOCK_FAIL_BUILD=1; then
  printf '构建失败后部署仍然成功\n' >&2
  exit 1
fi
assert_builds 1
test ! -s "$MOCK_COMPOSE_LOG"
reset_logs
run_deploy edge-mail
assert_builds 1

# 缺失构建输入必须阻止部署，不能对不完整内容生成可用标签。
mv "$fixture/edge-mail/app" "$fixture/edge-mail/app-missing"
reset_logs
if run_deploy edge-mail; then
  printf '缺失构建输入时部署仍然成功\n' >&2
  exit 1
fi
assert_builds 0
test ! -s "$MOCK_COMPOSE_LOG"

# 流水线必须交给服务脚本选择镜像，不能再用整个仓库的提交号覆盖内容标签。
if grep -Eq 'EDGE_(VISION|MEDIA|MAIL|LAYA)_IMAGE=.*SHORT_SHA' "$repo_root/.teamcity/settings.kts"; then
  printf 'TeamCity 仍使用全仓库提交号作为 edge 镜像标签\n' >&2
  exit 1
fi
printf 'edge 部署回归通过：无变化跳过构建、服务独立更新、配置复用镜像、失败停止部署\n'
