# edge-laya 运行镜像。
#
# 默认路径：从官方 python 基础镜像安装固定的 laya[serve]。国内网络下 Docker Hub
# 与 PyPI 可能不可达/极慢，因此基础镜像与 pip 源均可通过构建参数替换。
#
# CPU 推理镜像先安装 PyTorch CPU 版，避免通用索引拉取 CUDA 依赖。
ARG PYTHON_IMAGE=python:3.12-slim-bookworm
FROM ${PYTHON_IMAGE}

WORKDIR /app

# 换用国内 Debian 镜像源，避免构建时长时间卡在官方源。
RUN set -eux; \
    if [ -f /etc/apt/sources.list.d/debian.sources ]; then \
        sed -i 's|deb.debian.org|mirrors.aliyun.com|g; s|security.debian.org|mirrors.aliyun.com|g' /etc/apt/sources.list.d/debian.sources; \
    fi; \
    sed -i 's|deb.debian.org|mirrors.aliyun.com|g; s|security.debian.org|mirrors.aliyun.com|g' /etc/apt/sources.list 2>/dev/null || true

RUN apt-get update && apt-get install -y --no-install-recommends curl && rm -rf /var/lib/apt/lists/*

ARG LAYA_REF=d211a50dd816280e01f7782668f23000664c8a32
# 国内直连 PyPI 较慢，默认走清华源；需要时可用 PIP_INDEX_URL 覆盖。
ARG PIP_INDEX_URL=https://pypi.tuna.tsinghua.edu.cn/simple
# 基础镜像若已自带 laya[serve]（离线部署），把 SKIP_PIP_INSTALL 设为 1 直接复用。
ARG SKIP_PIP_INSTALL=0
ARG TORCH_CPU_INDEX=https://download.pytorch.org/whl/cpu
RUN if [ "${SKIP_PIP_INSTALL}" != "1" ]; then \
        pip install --no-cache-dir --index-url "${TORCH_CPU_INDEX}" --timeout 120 --retries 5 torch; \
    fi
RUN if [ "${SKIP_PIP_INSTALL}" != "1" ]; then \
        pip install --no-cache-dir --index-url "${PIP_INDEX_URL}" --timeout 120 --retries 5 \
            "laya[serve] @ https://github.com/NandhaKishorM/laya/archive/${LAYA_REF}.tar.gz"; \
    fi

COPY app /app/app

ENV HF_HUB_OFFLINE=1 TRANSFORMERS_OFFLINE=1 HF_HOME=/models LAYA_PORT=18082 LAYA_DEVICE=cpu LAYA_MODELS=english,multilingual LAYA_PRELOAD=1
EXPOSE 18082
CMD ["python", "-m", "app"]
