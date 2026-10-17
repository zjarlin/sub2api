FROM golang:1.26-alpine AS build
WORKDIR /src/vibex2api
COPY builtinlogin /src/builtinlogin
COPY vibex2api/go.mod vibex2api/go.sum ./
ARG GOPROXY=https://goproxy.cn,direct
ARG GOSUMDB=sum.golang.google.cn
ENV GOPROXY=${GOPROXY} GOSUMDB=${GOSUMDB}
RUN go mod download
COPY vibex2api/*.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/adapter .

FROM alpine:3.20
# 构建机在国内网络时可用 ALPINE_MIRROR 指向镜像站（例如 https://mirrors.tuna.tsinghua.edu.cn/alpine）。
ARG ALPINE_MIRROR=

RUN if [ -n "$ALPINE_MIRROR" ]; then \
      sed -i "s|https://dl-cdn.alpinelinux.org/alpine|$ALPINE_MIRROR|g" /etc/apk/repositories; \
    fi \
 && apk add --no-cache ca-certificates wget \
 && adduser -D -u 10001 app \
 && mkdir -p /app/data && chown -R app:app /app
COPY --from=build /out/adapter /app/adapter
USER app
EXPOSE 7866
ENTRYPOINT ["/app/adapter"]
