FROM golang:1.26.4-alpine

WORKDIR /app

# 安装必要的工具
RUN apk add --no-cache git chromium

ENV CHROME_BIN=/usr/bin/chromium-browser
ENV CHROMEDP_NO_SANDBOX=true

# 复制go.mod和go.sum
COPY go.mod go.sum ./

# 下载依赖
RUN go mod download

# 复制源代码
COPY . .

# 构建应用
RUN go build -o main ./cmd/server/

# 暴露端口
EXPOSE 8080

# 运行应用
CMD ["./main"]
