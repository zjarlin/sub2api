#!/usr/bin/env bash
#
# 在天津海光 DCU 机器上部署曼波 TTS + 开源视频配音流水线。
#
# 产物：
#   gpt-sovits:manbo   曼波 GPT-SoVITS 推理服务（9880，接入 sub2api 网络）
#   edge-dub:local     视频配音流水线（18084，ASR + 曼波 TTS + 对齐 + 回封）
#   edge-media:local   边缘媒体编排服务（18083，TTS 转发 / 配音任务）
#
# 前置：/opt/gptsovits-models/manbo 下已放好 GPT/SoVITS 权重与参考音频。
set -euo pipefail

REPO_DIR="${REPO_DIR:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)}"
STACK_DIR="${STACK_DIR:-/opt/edge-media-tianjin}"
NETWORK="${SUB2API_NETWORK:-sub2api_sub2api-network}"
MODELS_DIR="${GPT_SOVITS_MODELS_DIR:-/opt/gptsovits-models}"
DATA_DIR="${EDGE_MEDIA_DATA_DIR:-/opt/edge-media/data}"
DUB_DATA_DIR="${EDGE_DUB_DATA_DIR:-/opt/edge-dub/data}"
GPT_IMAGE="${GPT_SOVITS_IMAGE:-gpt-sovits:manbo}"
DUB_IMAGE="${EDGE_DUB_IMAGE:-edge-dub:local}"
MEDIA_IMAGE="${EDGE_MEDIA_IMAGE:-edge-media:local}"

docker network inspect "$NETWORK" >/dev/null 2>&1 || {
  echo "缺少 sub2api 网络 $NETWORK" >&2
  exit 1
}

echo "==> 构建镜像"
docker build -t "$GPT_IMAGE" -f "$REPO_DIR/edge-media/gptsovits/Dockerfile" "$REPO_DIR/edge-media/gptsovits"
docker build -t "$DUB_IMAGE" -f "$REPO_DIR/edge-media/dub/Dockerfile" "$REPO_DIR/edge-media/dub"
docker build -t "$MEDIA_IMAGE" -f "$REPO_DIR/edge-media/docker/Dockerfile" "$REPO_DIR/edge-media"

echo "==> 启动曼波 GPT-SoVITS"
GPT_SOVITS_IMAGE="$GPT_IMAGE" GPT_SOVITS_MODELS_DIR="$MODELS_DIR" SUB2API_NETWORK="$NETWORK" \
  docker compose -f "$REPO_DIR/edge-media/gptsovits/compose/docker-compose.yml" up -d

echo "等待 GPT-SoVITS 就绪（首次加载模型较慢）"
for _ in $(seq 1 60); do
  if docker exec gpt-sovits curl -fsS -G http://127.0.0.1:9880/ \
      --data-urlencode 'text=你好' --data-urlencode text_language=zh -o /dev/null 2>/dev/null; then
    echo "GPT-SoVITS 就绪"
    break
  fi
  sleep 5
done

echo "==> 启动视频配音流水线"
mkdir -p "$DUB_DATA_DIR"
EDGE_DUB_IMAGE="$DUB_IMAGE" MEDIA_TTS_UPSTREAM_URL=http://gpt-sovits:9880 SUB2API_NETWORK="$NETWORK" \
  EDGE_DUB_DATA_DIR="$DUB_DATA_DIR" \
  docker compose -f "$REPO_DIR/edge-media/dub/compose/docker-compose.yml" up -d

echo "==> 启动 edge-media 编排"
mkdir -p "$DATA_DIR"
EDGE_MEDIA_IMAGE="$MEDIA_IMAGE" EDGE_MEDIA_DATA_DIR="$DATA_DIR" EDGE_MEDIA_MODELS_DIR="$MODELS_DIR" \
  MEDIA_TTS_ENABLED=true MEDIA_TTS_UPSTREAM_URL=http://gpt-sovits:9880 \
  MEDIA_VIDEO_ENABLED=true MEDIA_VIDEO_UPSTREAM_URL=http://edge-dub:18084 \
  MEDIA_TRANSCODE_COMMAND='ffmpeg -hide_banner -y -i {input} -c:v libx264 -preset veryfast -c:a aac {output}' \
  SUB2API_NETWORK="$NETWORK" \
  docker compose -f "$REPO_DIR/edge-media/compose/docker-compose.yml" up -d --force-recreate

echo "==> 验证"
docker exec edge-media curl -fsS http://127.0.0.1:18083/health
echo
echo "部署完成。TTS 与配音链路：edge-media(18083) -> gpt-sovits(9880) / edge-dub(18084)"
