# OpenAI 出口代理专用镜像。
# mihomo 二进制与节点配置通过 bind mount 注入，镜像只提供 wget/CA 证书。
FROM nginx:1.27-alpine
LABEL org.opencontainers.image.title="sub2api-openai-egress"
LABEL org.opencontainers.image.description="Dedicated image for the OpenAI egress sidecar; mihomo is bind-mounted at /usr/local/bin/mihomo"
