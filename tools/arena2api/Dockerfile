FROM node:22-slim
ARG DEBIAN_MIRROR=
# 可按部署网络选择镜像；APT 仍校验发行版签名。
RUN if [ -n "$DEBIAN_MIRROR" ]; then sed -i "s|http://deb.debian.org|${DEBIAN_MIRROR%/}|g" /etc/apt/sources.list.d/debian.sources; fi && \
    apt-get -o Acquire::Retries=2 -o Acquire::http::Timeout=30 update && \
    apt-get -o Acquire::Retries=2 -o Acquire::http::Timeout=30 install -y --no-install-recommends ca-certificates tini && \
    rm -rf /var/lib/apt/lists/*
WORKDIR /app
ENV PLAYWRIGHT_BROWSERS_PATH=/ms-playwright
# Chromium 二进制必须走镜像：默认 CDN（cdn.playwright.dev -> prss.microsoft.com）在
# 国内构建网络下会长时间无响应，且 Playwright 自身没有兜底超时，曾在 252 上把整条
# 部署流水线挂死约 40 分钟（表现为 runc/oopBrowserDownload.js 静默 sleeping）。
# 覆盖下载主机并显式设置连接超时，让网络故障快速暴露而不是无限等待。
# Playwright 1.63 走 Chrome for Testing 路径 builds/cft/<version>/<platform>/...，
# npmmirror 已完整镜像该路径，且与官方 CDN 的 Content-Length 一致。
ARG PLAYWRIGHT_DOWNLOAD_HOST=https://registry.npmmirror.com/-/binary/playwright
ARG PLAYWRIGHT_DOWNLOAD_CONNECTION_TIMEOUT=30000
ENV PLAYWRIGHT_DOWNLOAD_HOST=${PLAYWRIGHT_DOWNLOAD_HOST} \
    PLAYWRIGHT_DOWNLOAD_CONNECTION_TIMEOUT=${PLAYWRIGHT_DOWNLOAD_CONNECTION_TIMEOUT}
COPY package.json package-lock.json ./
# --with-deps 会 apt 安装 Chromium 依赖库；浏览器本体由 playwright install chromium 下载。
# 外层加 3 次重试：镜像偶发抖动时自愈，仍失败则快速中止而不是挂到流水线超时。
RUN for attempt in 1 2 3; do \
      echo "playwright chromium install attempt ${attempt}" && \
      npm ci --ignore-scripts --no-audit --no-fund && \
      npx --no-install playwright install --with-deps chromium && \
      break || \
      { echo "attempt ${attempt} failed" >&2; [ "${attempt}" = "3" ] && exit 1; sleep 5; }; \
    done
COPY src ./src
RUN mkdir -p /app/data && chown -R node:node /app/data
USER node
ENV DATA_DIR=/app/data ARENA_HOST=0.0.0.0 ARENA_PORT=7867
EXPOSE 7867
ENTRYPOINT ["/usr/bin/tini", "--"]
CMD ["node", "src/index.mjs"]
