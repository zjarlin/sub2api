FROM node:22-slim
ARG DEBIAN_MIRROR=
# 可按部署网络选择镜像；APT 仍校验发行版签名。
RUN if [ -n "$DEBIAN_MIRROR" ]; then sed -i "s|http://deb.debian.org|${DEBIAN_MIRROR%/}|g" /etc/apt/sources.list.d/debian.sources; fi && \
    apt-get -o Acquire::Retries=2 -o Acquire::http::Timeout=30 update && \
    apt-get -o Acquire::Retries=2 -o Acquire::http::Timeout=30 install -y --no-install-recommends ca-certificates tini && \
    rm -rf /var/lib/apt/lists/*
WORKDIR /app
ENV PLAYWRIGHT_BROWSERS_PATH=/ms-playwright
COPY package.json package-lock.json ./
RUN npm ci --ignore-scripts --no-audit --no-fund && npx --no-install playwright install --with-deps chromium
COPY src ./src
RUN mkdir -p /app/data && chown -R node:node /app/data
USER node
ENV DATA_DIR=/app/data ARENA_HOST=0.0.0.0 ARENA_PORT=7867
EXPOSE 7867
ENTRYPOINT ["/usr/bin/tini", "--"]
CMD ["node", "src/index.mjs"]
