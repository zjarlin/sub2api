#!/usr/bin/env bash
#
# 在天津海光 DCU 机器上构建并部署曼波 TTS + 视频配音 + edge-media 编排。
#
# 这个脚本由 TeamCity 的 DeployTianjinMedia build type 在天津本机 agent 上调用，
# 也可以直接手工执行。它只负责“在天津这台机器上把三件套跑起来并把端口发布到
# 内网”，公网入口与鉴权/计费统一由 252 的 /media/* 承担。
#
# 前提：
#   - 天津已拉取到本仓库（CI 的 checkout 目录，或 STACK_DIR 下已有源码）
#   - /opt/gptsovits-models/manbo 下已放好 GPT/SoVITS 权重与参考音频
#   - 存在 sub2api_sub2api-network 这个 Docker 网络（天津的 sub2api 已经跑起来）
#
# 环境变量（都有默认值）：
#   REPO_DIR              源码根目录（默认脚本上两级）
#   GPT_SOVITS_MODELS_DIR 模型目录（默认 /opt/gptsovits-models）
#   EDGE_MEDIA_IMAGE      编排镜像 tag（默认 edge-media:tianjin）
#   GPT_SOVITS_IMAGE      曼波推理镜像 tag（默认 gpt-sovits:manbo-v6）
#   EDGE_DUB_IMAGE        配音镜像 tag（默认 edge-dub:tianjin）
#   MEDIA_TIANJIN_BIND    发布端口绑定的地址（默认 0.0.0.0）
set -euo pipefail

REPO_DIR="${REPO_DIR:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)}"
STACK_DIR="${STACK_DIR:-$REPO_DIR}"
NETWORK="${SUB2API_NETWORK:-sub2api_sub2api-network}"
MODELS_DIR="${GPT_SOVITS_MODELS_DIR:-/opt/gptsovits-models}"
DATA_DIR="${EDGE_MEDIA_DATA_DIR:-/opt/edge-media/data}"
DUB_DATA_DIR="${EDGE_DUB_DATA_DIR:-/opt/edge-dub/data}"
GPT_IMAGE="${GPT_SOVITS_IMAGE:-gpt-sovits:manbo-v6}"
DUB_IMAGE="${EDGE_DUB_IMAGE:-edge-dub:tianjin}"
MEDIA_IMAGE="${EDGE_MEDIA_IMAGE:-edge-media:tianjin}"
BIND="${MEDIA_TIANJIN_BIND:-0.0.0.0}"

PUBLISH_OVERLAY="$REPO_DIR/deploy/tianjin/docker-compose.edge-media-publish.yml"

docker network inspect "$NETWORK" >/dev/null 2>&1 || {
  echo "缺少 sub2api 网络 $NETWORK，请先在天津启动 sub2api 集群" >&2
  exit 1
}

test -s "$MODELS_DIR/manbo/GPT_weights_v2ProPlus/manbo-e15.ckpt" || {
  echo "缺少曼波 GPT 权重：$MODELS_DIR/manbo/GPT_weights_v2ProPlus/manbo-e15.ckpt" >&2
  exit 1
}
test -s "$MODELS_DIR/manbo/SoVITS_weights_v2ProPlus/manbo_e8_s904.pth" || {
  echo "缺少曼波 SoVITS 权重：$MODELS_DIR/manbo/SoVITS_weights_v2ProPlus/manbo_e8_s904.pth" >&2
  exit 1
}
test -s "$MODELS_DIR/manbo/reference/reference.mp3" || {
  echo "缺少曼波参考音频：$MODELS_DIR/manbo/reference/reference.mp3" >&2
  exit 1
}

echo "==> 构建曼波 GPT-SoVITS 镜像 $GPT_IMAGE"
docker build -t "$GPT_IMAGE" -f "$REPO_DIR/edge-media/gptsovits/Dockerfile" "$REPO_DIR/edge-media/gptsovits"

echo "==> 构建视频配音镜像 $DUB_IMAGE"
docker build -t "$DUB_IMAGE" -f "$REPO_DIR/edge-media/dub/Dockerfile" "$REPO_DIR/edge-media/dub"

echo "==> 构建 edge-media 编排镜像 $MEDIA_IMAGE"
docker build -t "$MEDIA_IMAGE" -f "$REPO_DIR/edge-media/docker/Dockerfile" "$REPO_DIR/edge-media"

echo "==> 启动曼波 GPT-SoVITS（发布 $BIND:9880）"
GPT_SOVITS_IMAGE="$GPT_IMAGE" GPT_SOVITS_MODELS_DIR="$MODELS_DIR" SUB2API_NETWORK="$NETWORK" \
  MEDIA_TIANJIN_BIND="$BIND" \
  docker compose -f "$REPO_DIR/edge-media/gptsovits/compose/docker-compose.yml" \
                 -f "$PUBLISH_OVERLAY" up -d

echo "等待 GPT-SoVITS 就绪（首次加载 DTK 模型可能 3~5 分钟）"
ready=0
for _ in $(seq 1 90); do
  if docker exec gpt-sovits curl -fsS -G http://127.0.0.1:9880/ \
      --data-urlencode 'text=你好' --data-urlencode text_language=zh -o /dev/null 2>/dev/null; then
    ready=1
    echo "GPT-SoVITS 已就绪"
    break
  fi
  sleep 5
done
if [ "$ready" != "1" ]; then
  echo "GPT-SoVITS 未在超时内就绪，最近日志：" >&2
  docker logs --tail 80 gpt-sovits >&2 || true
  exit 1
fi

echo "==> 启动视频配音流水线（发布 $BIND:18084）"
mkdir -p "$DUB_DATA_DIR"
EDGE_DUB_IMAGE="$DUB_IMAGE" MEDIA_TTS_UPSTREAM_URL=http://gpt-sovits:9880 SUB2API_NETWORK="$NETWORK" \
  EDGE_DUB_DATA_DIR="$DUB_DATA_DIR" MEDIA_TIANJIN_BIND="$BIND" \
  docker compose -f "$REPO_DIR/edge-media/dub/compose/docker-compose.yml" \
                 -f "$PUBLISH_OVERLAY" up -d

echo "==> 启动 edge-media 编排（发布 $BIND:18083，转发到本机 gpt-sovits / edge-dub）"
mkdir -p "$DATA_DIR"
EDGE_MEDIA_IMAGE="$MEDIA_IMAGE" EDGE_MEDIA_DATA_DIR="$DATA_DIR" EDGE_MEDIA_MODELS_DIR="$MODELS_DIR" \
  MEDIA_TTS_ENABLED=true MEDIA_TTS_UPSTREAM_URL=http://gpt-sovits:9880 \
  MEDIA_TTS_REFER_WAV=/models/manbo/reference/reference.mp3 \
  MEDIA_VIDEO_ENABLED=true MEDIA_VIDEO_UPSTREAM_URL=http://edge-dub:18084 \
  MEDIA_DUBBING_ENABLED=true \
  MEDIA_VIDEO_GENERATION_ENABLED="${MEDIA_VIDEO_GENERATION_ENABLED:-false}" \
  MEDIA_VIDEO_GENERATION_UPSTREAM_URL="${MEDIA_VIDEO_GENERATION_UPSTREAM_URL:-}" \
  MEDIA_TRANSCODE_COMMAND='ffmpeg -hide_banner -y -i {input} -c:v libx264 -preset veryfast -c:a aac -movflags +faststart {output}' \
  SUB2API_NETWORK="$NETWORK" MEDIA_TIANJIN_BIND="$BIND" \
  docker compose -f "$REPO_DIR/edge-media/compose/docker-compose.yml" \
                 -f "$PUBLISH_OVERLAY" up -d --force-recreate

echo "==> 验证"
for _ in $(seq 1 30); do
  status="$(docker inspect edge-media --format '{{.State.Health.Status}}' 2>/dev/null || echo missing)"
  [ "$status" = "healthy" ] && break
  sleep 2
done
docker exec edge-media curl -fsS http://127.0.0.1:18083/health
echo
echo "天津媒体链路：edge-media($BIND:18083) -> gpt-sovits(9880) / edge-dub(18084)"
