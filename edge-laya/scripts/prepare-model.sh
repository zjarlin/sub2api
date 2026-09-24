#!/usr/bin/env bash
#
# 准备 Laya 权重（英文 + 多语言）到本地目录。
#
#   edge-laya/scripts/prepare-model.sh /opt/sub2api/edge-laya/models
#
# 国内网络下 Hugging Face 与 Docker Hub 往往不可达，因此：
#   - 镜像默认走 EDGE_LAYA_PYTHON_IMAGE 指定的镜像源（可用已有的本地 tag）；
#   - 权重优先从 ModelScope 拉取同一 revision，失败再回退 Hugging Face；
#   - HF 场景可用 HF_ENDPOINT 指向镜像站（例如 https://hf-mirror.com）。
#
# 权重目录结构（挂载到容器 /models，只读）：
#   models/checkpoint/{model.safetensors,rl_agent_config.json,tokenizer,encoder}
#   models/checkpoint/multilingual/{model.safetensors,rl_agent_config.json,tokenizer,encoder}
set -euo pipefail

TARGET="${1:?usage: prepare-model.sh /absolute/models/directory}"
# ModelScope 与 Hugging Face 的 revision 体系独立：镜像站只有 master 分支，
# HF 侧则固定到已知快照，便于复现。
MODELSCOPE_REVISION="${EDGE_LAYA_MODELSCOPE_REVISION:-master}"
HF_REVISION="${EDGE_LAYA_HF_REVISION:-fe2b7719c095b82cdb2bfeedefc1fee30e506c21}"
PYTHON_IMAGE="${EDGE_LAYA_PYTHON_IMAGE:-python:3.12-slim-bookworm}"
PIP_INDEX_URL="${EDGE_LAYA_PIP_INDEX_URL:-https://pypi.tuna.tsinghua.edu.cn/simple}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"

mkdir -p "$TARGET"
TARGET="$(cd "$TARGET" && pwd -P)"

echo "准备 Laya 权重 (modelscope=$MODELSCOPE_REVISION, hf=$HF_REVISION) -> $TARGET"

docker run --rm \
  -e "MODELSCOPE_REVISION=$MODELSCOPE_REVISION" \
  -e "HF_REVISION=$HF_REVISION" \
  -e "PIP_INDEX_URL=$PIP_INDEX_URL" \
  -e "HF_ENDPOINT=${HF_ENDPOINT:-}" \
  -v "$TARGET:/models" \
  -v "$SCRIPT_DIR:/scripts:ro" \
  "$PYTHON_IMAGE" \
  sh -c '
    set -e
    pip install --quiet --no-cache-dir --index-url "$PIP_INDEX_URL" \
      "huggingface_hub>=0.20.0,<2" modelscope >/dev/null
    python /scripts/prepare_model.py
  '

for file in \
  "$TARGET/checkpoint/model.safetensors" \
  "$TARGET/checkpoint/rl_agent_config.json" \
  "$TARGET/checkpoint/multilingual/model.safetensors" \
  "$TARGET/checkpoint/multilingual/rl_agent_config.json"; do
  test -s "$file" || { echo "缺少 Laya 权重文件: $file" >&2; exit 1; }
done
echo "Laya 权重已准备: $TARGET"
