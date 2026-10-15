FROM mcr.microsoft.com/playwright:v1.62.0-noble
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci --ignore-scripts --no-audit --no-fund
COPY src ./src
RUN mkdir -p /app/data && chown -R pwuser:pwuser /app
USER pwuser
ENV TELEAGENT_HOST=0.0.0.0 TELEAGENT_PORT=7872 TELEAGENT_DATA_DIR=/app/data
EXPOSE 7872
CMD ["node", "src/index.mjs"]
