#!/usr/bin/env bash
#
# 在 252 上配置 `ssh tianjin-media`，让 TeamCity 的 DeployTianjinMedia 能经
# cloudflared 隧道在天津执行部署。252 与天津私网互不可达，唯一可用路径是
# cloudflared access ssh 到 tianjin.addzero.site。
#
# 前提：
#   - 252 已装 cloudflared（/usr/local/bin/cloudflared）
#   - 252 有 SSH 私钥，且其公钥已加入天津的 /root/.ssh/authorized_keys
#     （脚本会在天津侧打印需要追加的公钥）
#
# 用法（在 252 上执行）：
#   ./setup-252-tianjin-ssh.sh                # 配置并用现有密钥验证
#   SSH_KEY=/root/.ssh/tianjin-frp_ed25519 ./setup-252-tianjin-ssh.sh
set -euo pipefail

SSH_KEY="${SSH_KEY:-/root/.ssh/tianjin-frp_ed25519}"
REMOTE_HOST="${REMOTE_HOST:-tianjin.addzero.site}"
ALIAS="${ALIAS:-tianjin-media}"
CLOUDFLARED="${CLOUDFLARED:-/usr/local/bin/cloudflared}"

test -x "$CLOUDFLARED" || { echo "缺少 cloudflared: $CLOUDFLARED" >&2; exit 1; }
test -f "$SSH_KEY" || { echo "缺少 SSH 私钥: $SSH_KEY" >&2; exit 1; }

if ! grep -qE "^Host[[:space:]]+$ALIAS\$" /root/.ssh/config 2>/dev/null; then
  cat >> /root/.ssh/config <<CFG

Host $ALIAS
  HostName $REMOTE_HOST
  User root
  IdentityFile $SSH_KEY
  IdentitiesOnly yes
  ProxyCommand $CLOUDFLARED access ssh --hostname %h
  StrictHostKeyChecking no
  UserKnownHostsFile /root/.ssh/known_hosts
  ConnectTimeout 20
CFG
  echo "已写入 ~/.ssh/config 别名 $ALIAS"
fi

echo "==> 验证 ssh $ALIAS"
if ssh -o BatchMode=yes "$ALIAS" hostname; then
  echo "OK: 252 可以免密登录天津"
else
  echo "失败：请把下面的公钥追加到天津的 /root/.ssh/authorized_keys" >&2
  ssh-keygen -y -f "$SSH_KEY" >&2
  exit 1
fi
