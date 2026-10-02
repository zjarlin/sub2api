FROM node:22-slim
WORKDIR /app
COPY package.json ./
COPY src ./src
USER node
ENV WINDSURF_HOST=0.0.0.0 WINDSURF_PORT=7869
EXPOSE 7869
CMD ["node", "src/index.mjs"]
