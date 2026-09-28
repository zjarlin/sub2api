FROM mcr.microsoft.com/playwright:v1.59.1-noble
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci --omit=dev
COPY browser.mjs server.mjs ./
ENV ZCODE_VERIFY_HOST=0.0.0.0 ZCODE_VERIFY_HEADLESS=true ZCODE_VERIFY_PROFILE=/app/data
RUN mkdir -p /app/data && chown -R pwuser:pwuser /app
USER pwuser
EXPOSE 7866
CMD ["node", "server.mjs"]
