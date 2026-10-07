#!/bin/sh
set -eu
repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$repo_root"
node -e '
  const fs = require("fs");
  for (const file of ["deploy/hymt-frpc.toml", "deploy/hymt-frp-visitor.toml"]) {
    const config = fs.readFileSync(file, "utf8");
    for (const setting of ["transport.tcpMux = true", "transport.heartbeatInterval = 20", "transport.heartbeatTimeout = 90"]) {
      if (!config.includes(setting)) throw new Error(file + ": missing explicit FRP heartbeat configuration");
    }
  }
'
HYMT_DCU_API_KEY=test-key FRP_SERVER_ADDR=127.0.0.1 FRP_AUTH_TOKEN=test-token HYMT_STCP_KEY=test-stcp \
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
      check(!services["hymt-tunnel"].ports?.length, "STCP tunnel must not publish ports");
      console.log("Hy-MT2 DCU compose test passed");
    });'
