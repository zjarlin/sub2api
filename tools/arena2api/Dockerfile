FROM node:22-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates tini && rm -rf /var/lib/apt/lists/*
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
