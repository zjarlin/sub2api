FROM node:22-slim
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci --ignore-scripts --no-audit --no-fund
COPY src ./src
USER node
ENV CURSOR_HOST=0.0.0.0 CURSOR_PORT=7868
EXPOSE 7868
CMD ["node", "src/index.mjs"]
