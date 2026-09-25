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

默认媒体容器健康但不启用 TTS/视频生成。准备 GPT-SoVITS 后：

```bash
./edge-media/scripts/prepare-models.sh
MEDIA_TTS_ENABLED=true \
MEDIA_TTS_UPSTREAM_URL=http://gpt-sovits:9880 \
MEDIA_TTS_REFER_WAV=/models/manbo/reference/reference.mp3 \
  ./deploy/cluster/deploy-edge-media.sh
```

视频配音需要在接入服务中实现 ASR、说话人/人声分离、TTS、时间轴对齐和混音。可参考 MamboVideo 的流程，但不要把其 Windows 桌面依赖直接搬进 Linux 容器。
