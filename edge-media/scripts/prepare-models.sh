#!/usr/bin/env bash
# 准备曼波 GPT-SoVITS 模型素材。只下载公开权重与参考音频，不下载完整推理引擎。
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MODELS_DIR="${EDGE_MEDIA_MODELS_DIR:-$ROOT_DIR/models}"
MANBO_DIR="$MODELS_DIR/manbo"
BASE_URL="${MANBO_BASE_URL:-https://media.githubusercontent.com/media/MainRedstoner/Manbo-GPT-SoVITS/main}"

mkdir -p "$MANBO_DIR/GPT_weights_v2ProPlus" "$MANBO_DIR/SoVITS_weights_v2ProPlus" "$MANBO_DIR/reference"

download() {
  local url="$1" dest="$2"
  if [ -s "$dest" ]; then
    echo "复用 $dest"
    return
  fi
  echo "下载 $url"
  curl -fL --retry 4 --retry-delay 3 -o "$dest.tmp" "$url"
  mv "$dest.tmp" "$dest"
}

download "$BASE_URL/models/GPT_weights_v2ProPlus/manbo-e15.ckpt" \
  "$MANBO_DIR/GPT_weights_v2ProPlus/manbo-e15.ckpt"
download "$BASE_URL/models/SoVITS_weights_v2ProPlus/manbo_e8_s904.pth" \
  "$MANBO_DIR/SoVITS_weights_v2ProPlus/manbo_e8_s904.pth"
download "$BASE_URL/clone_source/manbo_clone_data/001.mp3" \
  "$MANBO_DIR/reference/reference.mp3"

printf '%s\n' "曼波模型素材已准备到 $MANBO_DIR"
printf '%s\n' "还需在目标机准备 GPT-SoVITS 引擎，并设置 MEDIA_TTS_UPSTREAM_URL 指向其 api.py。"
