# 开源视频配音流水线镜像：ffmpeg + faster-whisper(ASR) + Manbo TTS 调用。
#
# ASR 走 CPU（faster-whisper 的 CTranslate2 只支持 CUDA，海光 DCU 没有对应
# 后端；天津机器 128 核，int8 CPU 推理足够做离线配音），语音合成交给 DCU 上
# 的 GPT-SoVITS 上游。
#
# 天津访问 huggingface.co 不可达，模型经 hf-mirror.com 预下载进镜像。
ARG PYTHON_IMAGE=python:3.12-slim-bookworm
FROM ${PYTHON_IMAGE}

ENV DEBIAN_FRONTEND=noninteractive \
    PYTHONUNBUFFERED=1 \
    PIP_INDEX_URL=https://pypi.tuna.tsinghua.edu.cn/simple \
    PIP_DISABLE_PIP_VERSION_CHECK=1 \
    HF_ENDPOINT=https://hf-mirror.com \
    HF_HUB_DISABLE_XET=1 \
    OMP_NUM_THREADS=8

RUN set -eux; \
    if [ -f /etc/apt/sources.list.d/debian.sources ]; then \
      sed -i 's|deb.debian.org|mirrors.aliyun.com|g; s|security.debian.org|mirrors.aliyun.com|g' /etc/apt/sources.list.d/debian.sources; \
    fi; \
    sed -i 's|deb.debian.org|mirrors.aliyun.com|g; s|security.debian.org|mirrors.aliyun.com|g' /etc/apt/sources.list 2>/dev/null || true; \
    apt-get update; \
    apt-get install -y --no-install-recommends ffmpeg curl; \
    rm -rf /var/lib/apt/lists/*

RUN python3 -m pip install --no-cache-dir --timeout 120 --retries 5 \
      "faster-whisper>=1.0,<2" httpx "fastapi>=0.110" "uvicorn[standard]>=0.27"

# 预下载默认 ASR 模型，避免运行期访问 HuggingFace 失败。
ARG WHISPER_MODEL=medium
RUN python3 -c "from faster_whisper import WhisperModel; WhisperModel('${WHISPER_MODEL}', device='cpu', compute_type='int8')" && \
    python3 -c "import os, faster_whisper, glob; print('whisper cache', glob.glob(os.path.expanduser('~/.cache/huggingface/hub/*')))"

COPY pipeline /opt/dub/pipeline

ENV MEDIA_DUB_WHISPER_MODEL=${WHISPER_MODEL} \
    MEDIA_DUB_WHISPER_DEVICE=cpu \
    MEDIA_DUB_WHISPER_COMPUTE=int8

EXPOSE 18084
CMD ["python3", "/opt/dub/pipeline/dub_server.py"]
