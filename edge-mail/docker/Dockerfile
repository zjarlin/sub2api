# Edge Mail - 边缘计算邮件收发服务
#
# 只依赖 Python 标准库的 imaplib/smtplib/email 做协议交互，因此运行时不引入
# 额外系统库；镜像内不含任何账号或邮件数据。
ARG PYTHON_IMAGE=python:3.12-slim-bookworm
FROM ${PYTHON_IMAGE}

LABEL org.opencontainers.image.title="edge-mail" \
      org.opencontainers.image.description="边缘计算邮件服务：IMAP 收件 + SMTP 发件，支持 Outlook / Google / QQ" \
      org.opencontainers.image.source="https://github.com/zjarlin/sub2api"

WORKDIR /app

RUN set -eux; \
    if [ -f /etc/apt/sources.list.d/debian.sources ]; then \
      sed -i 's|deb.debian.org|mirrors.aliyun.com|g; s|security.debian.org|mirrors.aliyun.com|g' /etc/apt/sources.list.d/debian.sources; \
    fi; \
    sed -i 's|deb.debian.org|mirrors.aliyun.com|g; s|security.debian.org|mirrors.aliyun.com|g' /etc/apt/sources.list 2>/dev/null || true; \
    apt-get update; \
    apt-get install -y --no-install-recommends curl ca-certificates; \
    rm -rf /var/lib/apt/lists/*

ARG PIP_INDEX_URL=https://pypi.tuna.tsinghua.edu.cn/simple
COPY requirements.txt /app/requirements.txt
RUN pip install --no-cache-dir --index-url "${PIP_INDEX_URL}" --timeout 120 --retries 5 -r /app/requirements.txt

COPY app /app/app

ENV PORT=18084 WORKERS=1 TZ=Asia/Shanghai
EXPOSE 18084
CMD ["python", "-m", "app"]
