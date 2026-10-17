#!/usr/bin/env bash
#
# 在构建机（252 TeamCity agent）上配置 `ssh tianjin-frp` 别名。
#
# 天津与 252 私网不通，有两条路可走：
#   1. cloudflared access ssh（别名 tianjin-media）：握手常超时，实测约 20KB/s，
#      传 50MB 镜像要十几分钟 —— 不能用于镜像传输。
#   2. 天津 frpc 把本机 22 发布到 252 的 frps（本机 28083 端口）：实测约 6MB/s，
#      传镜像约 10 秒。流水线用这条。
#
# 用法（在 252 上执行）：
#   ./setup-tianjin-frp-ssh.sh
#   SSH_KEY=/root/.ssh/tianjin-frp_ed25519 FRP_PORT=28083 ./setup-tianjin-frp-ssh.sh
set -euo pipefail

SSH_KEY="${SSH_KEY:-/root/.ssh/tianjin-frp_ed25519}"
FRP_PORT="${FRP_PORT:-28083}"
ALIAS="${ALIAS:-tianjin-frp}"

test -f "$SSH_KEY" || { echo "缺少 SSH 私钥: $SSH_KEY" >&2; exit 1; }

if ! grep -qE "^Host[[:space:]]+$ALIAS\$" /root/.ssh/config 2>/dev/null; then
  cat >> /root/.ssh/config <<CFG

# 天津经 FRP（252 的 frps 把天津 22 发布到本机 $FRP_PORT）。
Host $ALIAS
  HostName 127.0.0.1
  Port $FRP_PORT
  User root
  IdentityFile $SSH_KEY
  IdentitiesOnly yes
  StrictHostKeyChecking no
  UserKnownHostsFile /root/.ssh/known_hosts
  ConnectTimeout 20
  ServerAliveInterval 30
  ServerAliveCountMax 3
CFG
  echo "已写入 ~/.ssh/config 别名 $ALIAS"
fi

echo "==> 验证 ssh $ALIAS"
if ssh -o BatchMode=yes "$ALIAS" hostname; then
  echo "OK: 构建机可以免密登录天津（FRP 通道）"
else
  echo "失败：请确认天津 frpc 已把 22 发布到 252:$FRP_PORT，且公钥在天津 authorized_keys" >&2
  ssh-keygen -y -f "$SSH_KEY" >&2
  exit 1
fi
