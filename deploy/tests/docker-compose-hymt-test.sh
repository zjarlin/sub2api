#!/bin/sh
set -eu
repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$repo_root"
POSTGRES_PASSWORD=compose-test-placeholder docker compose -f deploy/docker-compose.yml -f deploy/docker-compose.hymt.yml config --format json |
  node -e '
    let data = "";
    process.stdin.on("data", chunk => data += chunk);
    process.stdin.on("end", () => {
      const s = JSON.parse(data).services.hymt;
      const check = (ok, message) => { if (!ok) throw new Error(message); };
      check(s.image.includes("@sha256:"), "pin inference image digest");
      check(!s.ports?.length, "inference must not expose host ports");
      check(s.read_only === true, "read-only filesystem required");
      check(s.mem_limit === "4294967296" || s.mem_limit === 4294967296, "4 GiB memory bound required");
      check(Number(s.cpus) === 3, "3 CPU limit required");
      check(s.volumes.some(v => v.target === "/models" && v.read_only), "read-only model mount required");
      check(s.command.includes("/models/Hy-MT2-1.8B-Q8_0.gguf"), "use prepared offline model");
      check(!s.command.some(arg => arg.startsWith("--hf") || arg === "--model-url"), "no runtime model download");
      console.log("Hy-MT2 compose test passed");
    });'
