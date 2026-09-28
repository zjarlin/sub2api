FROM mcr.microsoft.com/playwright:v1.59.1-noble
WORKDIR /app
COPY package.json package-lock.json ./
RUN --mount=type=cache,id=zcode-electron-download,target=/root/.cache/electron npm ci --omit=dev
COPY browser.mjs electron.mjs server.mjs ./
ENV ZCODE_VERIFY_HOST=0.0.0.0 ZCODE_VERIFY_HEADLESS=true ZCODE_VERIFY_PROFILE=/app/data
RUN mkdir -p /app/data && chown -R pwuser:pwuser /app
USER pwuser
EXPOSE 7866
CMD ["xvfb-run", "-a", "./node_modules/electron/dist/electron", "--no-sandbox", "server.mjs"]
