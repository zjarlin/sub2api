# Edge Media - 边缘媒体服务

## 定位

- 独立于 Sub2API 主网关的边缘计算服务，部署在 252 Docker 主机上
- 提供曼波 / GPT-SoVITS 文本转语音、视频配音任务和视频生成任务编排
- 服务本身不内置大模型权重；通过 `MEDIA_*_UPSTREAM_URL` 或 `MEDIA_*_COMMAND` 接入外部推理服务
- 容器只加入 `sub2api_sub2api-network`，不暴露宿主端口；公网统一走 `18080 /media/*`

## 边界

- `POST /tts`、`POST /v1/audio/speech` 调用 GPT-SoVITS 兼容上游；曼波音色通过参考音频与权重在 GPT-SoVITS 侧配置
- `POST /videos/dub` 接收视频，调用 `MEDIA_DUBBING_COMMAND` 或视频上游的 `/dub`
- `POST /videos/generations` 调用 `MEDIA_VIDEO_GENERATION_COMMAND` 或视频生成上游
- `POST /videos/transcode` 使用镜像内 ffmpeg 做基础 H.264/AAC 转码
- 视频链路中的 ASR、人声分离、TTS、时间轴对齐和混音由接入的上游实现，服务只负责任务与文件边界

## 配置

- `MEDIA_TTS_ENABLED`：是否开放 TTS；默认 false
- `MEDIA_TTS_UPSTREAM_URL`：GPT-SoVITS `api.py` 基地址，例如 `http://gpt-sovits:9880`
- `MEDIA_TTS_REFER_WAV`、`MEDIA_TTS_PROMPT_TEXT`：曼波参考音频与参考文本
- `MEDIA_DUBBING_COMMAND`：视频配音命令，输入输出通过 `MEDIA_INPUT`、`MEDIA_OUTPUT` 传入
- `MEDIA_TRANSCODE_COMMAND`：可选，覆盖内置 ffmpeg 转码命令；同样使用 `MEDIA_INPUT`、`MEDIA_OUTPUT`
- `MEDIA_VIDEO_GENERATION_COMMAND` / `MEDIA_VIDEO_GENERATION_UPSTREAM_URL`：视频生成后端
- `MEDIA_DATA_DIR`：任务输入输出持久化目录，默认 `/data`

## 模型

- `scripts/prepare-models.sh` 只下载公开的曼波 GPT-SoVITS 权重和参考音频，不提交到 Git
- GPT-SoVITS 引擎、Faster-Whisper、UVR5、视频生成权重等大依赖由部署主机单独准备
- 252 当前无 NVIDIA GPU；TTS/视频重模型建议放在有 GPU 的内网服务，或明确接受 CPU 延迟后再启用

## 验证

```bash
python3 -m pytest -q edge-media/tests
docker build -f edge-media/docker/Dockerfile edge-media
```

有真实上游时另测：

```bash
curl -fsS -X POST http://192.168.31.252:18080/media/tts \
  -H "Authorization: Bearer $CODEX_GROUP_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"text":"你好，我是曼波。","language":"zh","response_format":"wav"}' \
  -o manbo.wav
```

## 天津 GPU 部署（海光 DCU）

- 252 无 GPU，只做编排与公开入口；重模型放在天津 `tianjin.addzero.site`（2× 海光 DCU）
- `gptsovits/`：曼波 GPT-SoVITS 推理镜像，含 DTK 兼容的 torchaudio shim 与源码补丁
- `dub/`：开源视频配音流水线（faster-whisper ASR + 曼波 TTS + 对齐 + ffmpeg 回封）
- `scripts/deploy-tianjin-gpu.sh`：一键构建并启动三件套
- 参考音频必须裁到 3~10 秒，且 `prompt_text` 与裁剪片段对应，否则合成接近静音
- 天津访问 GitHub / HuggingFace 受限：GPT-SoVITS 走 gh-proxy，模型走 ModelScope，
  whisper 模型走 hf-mirror 且需 `HF_HUB_DISABLE_XET=1`

## 双端流水线

- TeamCity 源在 `.teamcity/settings.kts`（versioned settings 的唯一事实源）：
  - `Deploy252Cluster`：在 252 agent 上构建 sub2api 镜像并部署双副本 + edge-vision + edge-media
  - `DeployTianjinMedia`：在天津 agent 上源码构建并启动曼波 TTS / 配音 / edge-media
- 两边都挂 default 分支的 vcsTrigger，推送即同时部署
- 天津镜像（GPT-SoVITS 基础镜像 + 权重）体积很大，**在天津本机构建**，不跨隧道搬运
- 网关媒体开关（`GATEWAY_MEDIA_ENABLED` / `GATEWAY_VISION_ENABLED`）由
  `deploy/docker-compose.edge-media.yml` 注入，`SUB2API_EDGE_MEDIA=1` 时叠加
- 252 → 天津只走 FRP 或 cloudflared：两边私网互不可达，且天津无公网 IPv6

## 视频生成

- 不使用离线视频生成模型；走网络 API（Seedance 2.0 / Ark）
- 平台原生实现：`backend/internal/service/seedance.go`、`handler/seedance.go`
- 路由：`/v1|/api/v3/contents/generations/tasks`（POST 创建、GET 轮询、DELETE 取消）
- 账号需勾选 `Seedance (Ark)` 能力并配置 Ark 地址与 Key
