#!/bin/sh
# 从 .env 渲染 stream 代理配置（唯一默认值来源是 .env）
set -e
: "${HYMT_MODEL_UPSTREAM_HOST:?HYMT_MODEL_UPSTREAM_HOST is required}"
: "${HYMT_MODEL_UPSTREAM_PORT:?HYMT_MODEL_UPSTREAM_PORT is required}"
: "${HYMT_MODEL_BIND_HOST:?HYMT_MODEL_BIND_HOST is required}"
: "${HYMT_MODEL_BIND_PORT:?HYMT_MODEL_BIND_PORT is required}"
envsubst '${HYMT_MODEL_UPSTREAM_HOST} ${HYMT_MODEL_UPSTREAM_PORT} ${HYMT_MODEL_BIND_HOST} ${HYMT_MODEL_BIND_PORT}' \
  < /etc/nginx/hymt-stream.conf.template > /tmp/nginx.conf
exec nginx -c /tmp/nginx.conf -g 'daemon off;'
