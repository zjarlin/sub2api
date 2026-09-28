import { adapterConfig, createRuntime } from "./runtime.mjs";
import { createServer } from "./server.mjs";

const config = adapterConfig();
const runtime = createRuntime(config);
const server = createServer({ config, runtime });
server.listen(config.port, config.host, () => {
  console.log(JSON.stringify({ event: "arena_adapter_started", url: `http://${config.host}:${config.port}`, dataDir: config.dataDir, status: runtime.health().status }));
});
for (const signal of ["SIGINT", "SIGTERM"]) {
  process.once(signal, async () => {
    await server.stop();
  });
}
