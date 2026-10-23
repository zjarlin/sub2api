#!/bin/sh
set -eu
repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$repo_root"
HYMT_DCU_API_KEY=test-key \
  docker compose -f deploy/docker-compose.hymt-tianjin.yml config --format json |
  node -e '
    let data = "";
    process.stdin.on("data", chunk => data += chunk);
    process.stdin.on("end", () => {
      const services = JSON.parse(data).services;
      const s = services.hymt;
      const check = (ok, message) => { if (!ok) throw new Error(message); };
      check(s.ports.length === 1 && s.ports[0].host_ip === "127.0.0.1", "inference must bind loopback only");
      check(s.environment.HIP_VISIBLE_DEVICES === "1", "default to second DCU");
      check(s.environment.HF_HUB_OFFLINE === "1" && s.environment.TRANSFORMERS_OFFLINE === "1", "offline inference required");
      check(s.environment.VLLM_API_KEY === "test-key", "API key required");
      check(s.command[s.command.indexOf("--gpu-memory-utilization") + 1] === "0.12", "bounded memory budget required");
      check(s.command.includes("--enforce-eager"), "avoid graph memory overhead");
      check(s.command[s.command.indexOf("--served-model-name") + 1] === "hy-mt2", "model alias must match adapter");
      // 公网 FRP 隧道已退役；模型经 EasyTier + hymt-relay 访问，天津侧不得再暴露端口。
      check(!services["hymt-tunnel"], "hymt-dcu must not ship a public FRP tunnel service");
      console.log("Hy-MT2 DCU compose test passed");
    });'
