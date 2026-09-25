# edge-dub · 开源视频配音流水线

把上传视频自动做成曼波配音：**ASR → 曼波 TTS → 时间轴对齐 → 回封**。
完全基于开源组件，跑在天津 GPU 机器上，供 edge-media 通过
`MEDIA_VIDEO_UPSTREAM_URL` 调用。

## 组件

- **ffmpeg**：抽音轨、补静音、变速、拼接、回封
- **faster-whisper**：语音识别（CPU int8；CTranslate2 只支持 CUDA，海光 DCU
  没有对应后端，天津机器 128 核 CPU 推理足够做离线配音）
- **GPT-SoVITS（曼波）**：逐段语音合成，通过 `MEDIA_TTS_UPSTREAM_URL` 调用
  部署在同机的 GPT-SoVITS 服务

## 流程

1. ffmpeg 抽出 16kHz 单声道音轨
2. faster-whisper 转写并给出每段起止时间
3. 逐段调 GPT-SoVITS 合成曼波语音
4. `fit_audio` 把每段贴到目标时长：短了补静音，长了用 `atempo` 加速（封顶 1.6x）
5. 按原时间轴拼接配音轨，与原视频混音回封（默认压低原声保留环境音）

## 构建

```bash
docker build -t edge-dub:local -f edge-media/dub/Dockerfile edge-media/dub
```

镜像内预置 faster-whisper `medium` 模型（经 `hf-mirror.com`，并关闭 `hf-xet`
避免 CAS 401）；`WHISPER_MODEL` 构建参数可换成 `small`/`large-v3`。

## 运行

```bash
MEDIA_TTS_UPSTREAM_URL=http://gpt-sovits:9880 \
SUB2API_NETWORK=sub2api_sub2api-network \
  docker compose -f edge-media/dub/compose/docker-compose.yml up -d
```

## 两种调用方式

**HTTP（给 edge-media 用）**：

```bash
curl -fsS -X POST http://127.0.0.1:18084/dub \
  -H 'X-Media-Filename: input.mp4' \
  --data-binary @input.mp4 -o dubbed.mp4
```

**命令行（一次性任务）**：

```bash
docker run --rm --network sub2api_sub2api-network \
  -v /path/input.mp4:/in.mp4:ro -v /tmp/dubout:/out \
  -e MEDIA_INPUT=/in.mp4 -e MEDIA_OUTPUT=/out/output.mp4 \
  -e MEDIA_TTS_UPSTREAM_URL=http://gpt-sovits:9880 \
  --entrypoint python3 edge-dub:tianjin /opt/dub/pipeline/dub_video.py
```

## 环境变量

| 变量 | 说明 |
| --- | --- |
| `MEDIA_TTS_UPSTREAM_URL` | 曼波 GPT-SoVITS 基地址 |
| `MEDIA_DUB_LANGUAGE` | 识别与合成语种，默认 `zh` |
| `MEDIA_DUB_WHISPER_MODEL` | ASR 模型，默认 `medium` |
| `MEDIA_DUB_WHISPER_COMPUTE` | 计算精度，CPU 默认 `int8` |
| `MEDIA_DUB_KEEP_ORIGINAL` | 是否保留原声混音，默认 `false` |

## 接入 edge-media

edge-media 的 `/videos/dub` 在配置 `MEDIA_VIDEO_UPSTREAM_URL=http://edge-dub:18084`
后，会把上传视频 POST 到本服务的 `/dub`，返回的 output.mp4 作为任务产物。
