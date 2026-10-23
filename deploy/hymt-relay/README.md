# hymt-relay：把天津 Hy-MT2 接进网关容器网络

离线翻译提供方 `hymt` 的模型（Hy-MT2，vLLM）跑在天津 GPU 机 `tianjin-ai` 的
`127.0.0.1:18086`，而网关容器只能访问 Docker 网络里的地址。两层之间的唯一通路是
EasyTier 组网（182 `et-edge` ↔ 天津 `et-tianjin-ai`），本目录就是这条通路的两端配置。

```
sub2api 容器 ──opt_default──▶ hymt-relay:18086 ──EasyTier──▶ 10.144.144.97:18086 ──▶ 127.0.0.1:18086 (vLLM)
```

## 为什么不再用 FRP

历史上走公网 FRP（天津 frpc → `61.163.60.12:7000` → 网关侧 `hymt-visitor`）。FRP 服务端随
旧主机退役后这条链路整段失效，而 `translate_providers.hymt.base_url` 仍指向不存在的
`http://hymt-visitor:18086`，于是 DNS 立即失败（`latency_ms≈1`）→
`translation_providers_failed`。EasyTier 已经是 182 与天津之间唯一在用的通路，直接复用它。

## 天津侧（`tianjin-ai`）

`/root/DockerCompose/easytier-tianjin-ai/compose.yml` 里的端口转发参数：

```yaml
- --port-forward=tcp://${ET_MYMT_BIND}/127.0.0.1:18086   # ET_MYMT_BIND=10.144.144.97:18086
```

只绑定在 EasyTier 地址上，不发布到宿主公网/内网 IP；模型鉴权仍由 `HYMT_DCU_API_KEY` 负责。
改完 `docker compose up -d` 即可，规则随进程持久化（不再是运行时临时规则）。

验证（天津机上）：

```bash
ss -ltn | grep 18086            # 应看到 10.144.144.97%tun0:18086
```

## 182 侧

部署目录 `/root/DockerCompose/hymt-relay/`：

| 文件 | 说明 |
|---|---|
| `.env` | 唯一配置源（从 `.env.example` 复制；写 EasyTier 上天津节点地址） |
| `docker-compose.yml` / `entrypoint.sh` / `hymt-stream.conf.template` | 本目录文件，同步即可 |

```bash
install -d -m 700 /root/DockerCompose/hymt-relay
cp deploy/hymt-relay/{docker-compose.yml,entrypoint.sh,hymt-stream.conf.template} /root/DockerCompose/hymt-relay/
cp deploy/hymt-relay/.env.example /root/DockerCompose/hymt-relay/.env   # 填 HYMT_MODEL_UPSTREAM_HOST
cd /root/DockerCompose/hymt-relay && docker compose up -d
```

容器接入的是网关的 `opt_default` 网络，因此网关侧只需把提供方地址指向容器名：

```
translate_providers.hymt.base_url = http://hymt-relay:18086
```

（后台「设置 → 翻译服务商」里改，或直接改 `settings` 表；改完无需重启网关，聚合器按配置
指纹自动重建。）

## 验证

```bash
# 1) 转发层到模型（401 = 已打通到 vLLM，鉴权正常）
docker exec sub2api wget -q -O /dev/null http://hymt-relay:18086/v1/models ; echo rc=$?

# 2) 网关端到端（应返回 200 且响应头 X-Translation-Provider: hymt）
curl -sS -D - -o /dev/null http://127.0.0.1:18080/v1/responses \
  -H "Authorization: Bearer $SUB2API_API_KEY" -H 'Content-Type: application/json' \
  -d '{"model":"translate","input":"Hello world","metadata":{"target_language":"zh"}}' \
  | grep -i 'HTTP/\|x-translation-provider'

# 3) 提供方健康分（hymt successes 递增、consecutive_failures 归零）
curl -s http://127.0.0.1:18080/api/v1/translate/providers
```

## 已知边界

- 这是**明文 TCP 透传**：流量只经过 Docker 桥和 EasyTier 加密隧道，不走公网明文；
  但 182 上任何能进 `opt_default` 的容器都能访问该端口，鉴权只靠 vLLM 的 Bearer Key。
- EasyTier 断连时 `hymt` 会失败并回退；网关不会因该提供方不可用而拒绝服务，
  `/health` 与其它提供方不受影响。
