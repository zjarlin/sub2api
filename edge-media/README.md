# Edge Media

边缘媒体服务，接入 Sub2API 统一鉴权与计费：

- `POST /media/tts`：曼波 / GPT-SoVITS 文本转语音
- `POST /media/videos/dub`：视频配音任务
- `POST /media/videos/generations`：视频生成任务
- `POST /media/videos/transcode`：基础视频转码

## 本地验证

```bash
python3 -m pytest -q edge-media/tests
```

## 端点

| 端点 | 说明 |
| --- | --- |
| `POST /tts`、`POST /v1/audio/speech` | 曼波/GPT-SoVITS 文本转语音，支持 `text`/`input`/`messages`、`language`、`response_format`、`return_base64` |
| `POST /videos/dub` | `multipart/form-data`，字段 `video`（文件）与 `options`（JSON 字符串） |
| `POST /videos/generations` | JSON 请求体透传到视频生成上游或命令 |
| `POST /videos/transcode` | `multipart/form-data`，字段 `video` 与 `options`，内置 ffmpeg H.264/AAC |
| `GET /tasks/{task_id}` | 查询任务状态（状态持久化在 `/data/tasks/<id>/task.json`） |
| `GET /tasks/{task_id}/content` | 下载任务产物 |

## 配置

所有模型都通过环境变量接入，容器本身不含权重：

- `MEDIA_TTS_ENABLED` / `MEDIA_TTS_UPSTREAM_URL`：开启 TTS 及 GPT-SoVITS `api.py` 基地址。
- `MEDIA_TTS_REFER_WAV` / `MEDIA_TTS_PROMPT_TEXT` / `MEDIA_TTS_PROMPT_LANGUAGE`：曼波参考音频与参考文本。
- `MEDIA_DUBBING_ENABLED` / `MEDIA_DUBBING_COMMAND` / `MEDIA_VIDEO_UPSTREAM_URL`：视频配音命令或视频上游 `/dub`。
- `MEDIA_VIDEO_GENERATION_ENABLED` / `MEDIA_VIDEO_GENERATION_COMMAND` / `MEDIA_VIDEO_GENERATION_UPSTREAM_URL`：视频生成后端。
- `MEDIA_TRANSCODE_COMMAND`：可选，覆盖内置 ffmpeg 转码命令。
- `MEDIA_MAX_UPLOAD_BYTES` / `MEDIA_DATA_DIR`：上传上限和任务数据目录。

命令类后端统一通过 `MEDIA_INPUT`、`MEDIA_OUTPUT`、`MEDIA_TASK_ID`、`MEDIA_METADATA_JSON` 等环境变量获取输入输出路径。

部署到 252：

```bash
EDGE_MEDIA_IMAGE=zjarlin/edge-media:local \
  ./deploy/cluster/deploy-edge-media.sh
```

默认媒体容器健康但不启用 TTS/视频生成。

### 天津 GPU 部署（曼波 TTS + 视频配音）

252 无 GPU，只做编排与公网入口；曼波 TTS 与视频配音流水线跑在天津海光 DCU 机器上。
天津是三套重模型服务（`gpt-sovits` 9880 / `edge-dub` 18084 / `edge-media` 18083），
通过 FRP 或 cloudflared 把内网端口暴露给 252：

```bash
# 需要先在天津机器准备好 /opt/gptsovits-models/manbo 权重与参考音频
./edge-media/scripts/deploy-tianjin-gpu.sh
```

三件套：`gpt-sovits`(9880) 曼波推理、`edge-dub`(18084) 视频配音流水线、
`edge-media`(18083) 编排。详见 [gptsovits/README.md](gptsovits/README.md) 与
[dub/README.md](dub/README.md)。

#### 252 → 天津连接（二选一，或都配）

252 与天津不在同一内网，且都无法直连对方私网。两条可用路径：

1. **FRP（推荐，适合大视频）**：天津（或能访问 GPU 机器的中控机）跑 `frpc`
   连到 252 的 `frps`（公网 `61.163.60.12:7000`，需把 `28084/28085` 加入
   `allowPorts`），把 `edge-media:18083`、`gpt-sovits:9880` 映射出去。
   模板见 [../deploy/tianjin/frpc-media.toml.template](../deploy/tianjin/frpc-media.toml.template)。
2. **cloudflared**：天津本机 cloudflared 隧道加 `media-tj` / `tts-tj` / `dub-tj`
   主机名，252 走 HTTPS 调用。模板见
   [../deploy/tianjin/cloudflared-media-config.yml.template](../deploy/tianjin/cloudflared-media-config.yml.template)。

252 侧只需把 upstream 指向对应地址：

```bash
MEDIA_TTS_ENABLED=true \
MEDIA_TTS_UPSTREAM_URL=http://host.docker.internal:28085 \
MEDIA_VIDEO_ENABLED=true \
MEDIA_VIDEO_UPSTREAM_URL=http://host.docker.internal:28084 \
SUB2API_EDGE_MEDIA=1 \
  ./deploy/cluster/deploy-edge-media.sh
```

FRP 端口在 252 的 loopback 上侦听；edge-media 运行在容器内，需通过
`host.docker.internal` 访问宿主的 FRP 端口（公网出口 `61.163.60.12` 不支持 hairpin NAT）。

### 视频生成（网络 API）

视频生成**不用离线模型**，直接走平台的 Seedance / Ark 异步任务协议：

```
POST /v1/contents/generations/tasks        # 创建任务
GET  /v1/contents/generations/tasks/{id}   # 轮询状态
```

账号侧在「账号 → OpenAI 能力」勾选 `Seedance (Ark)` 并填 Ark 基地址与 Key 即可，
网关会按 Ark 的实际 completion tokens 计费。`MEDIA_VIDEO_GENERATION_ENABLED`
保持 `0`：视频生成不经过 edge-media 的离线转发，而是直接调用上面的原生端点。

### 252 接入 GPT-SoVITS 后

```bash
./edge-media/scripts/prepare-models.sh
MEDIA_TTS_ENABLED=true \
MEDIA_TTS_UPSTREAM_URL=http://gpt-sovits:9880 \
MEDIA_TTS_REFER_WAV=/models/manbo/reference/reference.mp3 \
  ./deploy/cluster/deploy-edge-media.sh
```

视频配音需要在接入服务中实现 ASR、说话人/人声分离、TTS、时间轴对齐和混音。可参考 MamboVideo 的流程，但不要把其 Windows 桌面依赖直接搬进 Linux 容器。
