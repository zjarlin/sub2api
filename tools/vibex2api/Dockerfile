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
RUN apk add --no-cache ca-certificates wget \
 && adduser -D -u 10001 app \
 && mkdir -p /app/data && chown -R app:app /app
COPY --from=build /out/adapter /app/adapter
USER app
EXPOSE 7866
ENTRYPOINT ["/app/adapter"]
