FROM golang:1.26-alpine AS build
WORKDIR /src
ARG GOPROXY=https://goproxy.cn,direct
ARG GOSUMDB=sum.golang.google.cn
ENV GOPROXY=${GOPROXY} GOSUMDB=${GOSUMDB}
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/adapter .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates chromium tzdata wget \
 && adduser -D -u 10001 app && mkdir -p /app && chown -R app:app /app
WORKDIR /app
COPY --from=build /out/adapter /app/adapter
ENV CGW_BROWSER_EXECUTABLE=/usr/bin/chromium
USER app
EXPOSE 7881
ENTRYPOINT ["/app/adapter"]
