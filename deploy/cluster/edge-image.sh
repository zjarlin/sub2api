#!/usr/bin/env bash

# 对镜像实际复制的目录、Dockerfile 和构建参数取摘要；部署配置不参与镜像版本。
edge_image_fingerprint() {
  local context="$1"
  local build_args="$2"
  shift 2
  (
    cd "$context"
    local inputs=(docker/Dockerfile app)
    local optional
    for optional in requirements.txt .dockerignore docker/Dockerfile.dockerignore; do
      if [ -f "$optional" ]; then
        inputs+=("$optional")
      fi
    done
    inputs+=("$@")
    {
      printf '%s\0' "$build_args"
      # 显式排序并抹去时间与属主，兼容 252 上不支持 --sort 的旧版 GNU tar。
      find "${inputs[@]}" -print0 | LC_ALL=C sort -z |
        tar --null --no-recursion --files-from=- --format=gnu \
          --mtime=@0 --owner=0 --group=0 --numeric-owner -cf -
    } | sha256sum | cut -d ' ' -f 1
  )
}

edge_ensure_image() {
  local image="$1"
  local context="$2"
  shift 2
  if [ "${EDGE_FORCE_BUILD:-0}" != "1" ] && docker image inspect "$image" >/dev/null 2>&1; then
    echo "复用已有边缘镜像 $image，跳过构建"
    return
  fi
  echo "构建边缘镜像 $image"
  docker build -t "$image" -f "$context/docker/Dockerfile" "$@" "$context"
}
