# 国内网络下 Docker Hub 与 Debian 官方源常不可达/缓慢，两者都做成可替换。
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
RUN pip install --no-cache-dir "laya[serve] @ https://github.com/NandhaKishorM/laya/archive/${LAYA_REF}.tar.gz"
COPY app /app/app

ENV HF_HUB_OFFLINE=1 TRANSFORMERS_OFFLINE=1 HF_HOME=/models LAYA_PORT=18082 LAYA_DEVICE=cpu LAYA_MODELS=english,multilingual LAYA_PRELOAD=1
EXPOSE 18082
CMD ["python", "-m", "app"]
