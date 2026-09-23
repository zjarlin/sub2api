FROM python:3.12-slim-bookworm

LABEL org.opencontainers.image.title="edge-vision" \
      org.opencontainers.image.description="离线边缘计算视觉服务：YOLO 检测/分割/姿态/分类 + OCR" \
      org.opencontainers.image.source="https://github.com/zjarlin/sub2api"

ENV PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    MODEL_DIR=/models \
    PORT=18081 \
    WORKERS=1 \
    ORT_NUM_THREADS=4 \
    PIP_INDEX_URL=https://pypi.tuna.tsinghua.edu.cn/simple \
    PIP_DEFAULT_TIMEOUT=120 \
    PIP_RETRIES=5

WORKDIR /app

# 使用清华 Debian 镜像源，避免默认源在国内拉取缓慢
RUN set -eux; \
    if [ -f /etc/apt/sources.list.d/debian.sources ]; then \
        sed -i 's|deb.debian.org|mirrors.tuna.tsinghua.edu.cn|g; s|security.debian.org|mirrors.tuna.tsinghua.edu.cn|g' /etc/apt/sources.list.d/debian.sources; \
    fi

# 运行期系统依赖（OpenCV / ONNX Runtime 所需的最小库集合）
RUN apt-get update && apt-get install -y --no-install-recommends \
        libgl1 \
        libglib2.0-0 \
        libgomp1 \
        libsm6 \
        libxext6 \
        libxrender1 \
        curl \
    && rm -rf /var/lib/apt/lists/*

# Python 依赖（构建期需联网；运行期完全离线）
COPY requirements.txt ./
RUN pip install --no-cache-dir --upgrade pip && \
    pip install --no-cache-dir -r requirements.txt

# 应用代码
COPY app ./app

# 离线模型：YOLOv8 ONNX，全部随镜像打包，运行时不下载
COPY models /models

EXPOSE 18081

HEALTHCHECK --interval=30s --timeout=10s --start-period=20s --retries=3 \
    CMD curl -fsS http://127.0.0.1:18081/health || exit 1

CMD ["python3", "-m", "app"]
