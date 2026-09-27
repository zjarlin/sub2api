# 视频配音

支持两种输入，都输出完整的 H.264/AAC MP4：

| mode | 输入 | 流程 |
| --- | --- | --- |
| auto（默认） | 自带清晰单人讲话的视频 | 提取音轨 → faster-whisper 转写及时间戳 → 曼波 GPT-SoVITS 合成 → 对齐回封 |
| timeline | 无声或有声视频 + 时间轴文字 | 读取 segments → 曼波逐段合成 → 按指定时间轴回封，不做 ASR |

这是文字驱动的重新朗读，不是直接转换原声音波。不会完整保留原人物的语调和情绪。
当前不含翻译、多人音色分配、人声分离、声音克隆训练、口型重建或 SRT 解析。
语言选项只选择识别/合成语言，不会翻译文字。默认使用服务配置的曼波音色。

## 网页操作

在「边缘计算服务」选择 `/media/videos/dub`，请求表单直接打开：

1. 选择有余额且绑定可用分组的 API Key，上传本机视频。
2. 选择「原声自动配音」或「时间轴配音」。
3. 时间轴模式逐段填写开始秒数、结束秒数和文字，也可导入完整 options JSON。
4. 选择语言；「混入 15% 原声」会包含原人声，不等于分离背景音乐。
5. 点击发送，等待任务完成。成功后可播放和下载视频，失败时查看具体错误。
6. 记录 task_id，可再次查询和下载。请求示例与表单同步，真实密钥不会写入示例。

「接口文档」标签包含字段说明、两种流程及下载命令。TTS 的二进制音频也能直接播放、下载。

## 参数

公开接口：`POST /media/videos/dub`，使用 `multipart/form-data`。

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| video | 文件，必填 | 默认最大 512 MiB，支持 MP4/MOV/MKV/WebM 等 ffmpeg 可读取的视频 |
| options | JSON 文本 | 默认 `{}`；不是嵌套的 HTTP JSON 请求体 |

options 字段：

| 字段 | 类型 / 默认值 | 说明 |
| --- | --- | --- |
| mode | 字符串 / auto | auto 或 timeline |
| language | 字符串 / zh | zh、en、ja、ko、yue；需与讲话/文字语言一致 |
| keep_original_audio | 布尔值 / false | false 替换原音轨；true 以 15% 音量混入整条原音轨，无声视频忽略此项 |
| segments | 数组 / 无 | timeline 必填；auto 不接受该字段 |

每个 segment 必须且只能包含 `start`、`end`、`text`：
时间是有限数字，单位为秒，支持小数；`0 <= start < end <= 视频时长`；
按开始时间排列且不重叠。1–500 个片段，每段 1–2000 字符，不能全为空白。
未知参数会报错，避免拼错字段后悄悄忽略。

合成较短时补静音；较长时最多加速 1.6 倍，仍无法容纳会指出片段编号和所需时长，
请减少文字或延长时间段，不会直接裁掉超长句尾。开头、间隙、最后一句后的画面保留。

## 自动模式示例

先在终端设置密钥及本机文件路径：

```bash
export SUB2API_KEY='替换为完整API Key'
VIDEO_FILE='/absolute/path/input.mp4'
curl --fail-with-body --show-error \
  'http://192.168.31.252:18080/media/videos/dub' \
  -H "Authorization: Bearer $SUB2API_KEY" \
  -F "video=@${VIDEO_FILE};type=video/mp4" \
  --form-string 'options={"mode":"auto","language":"zh","keep_original_audio":false}' \
  -o dub-task.json
```

`@` 表示读取本机文件。认证头用双引号才能展开变量。不要手动设置 multipart boundary。
`--form-string` 将 JSON 当作普通文本上传，即使配音文字中含 `&` 也不会拆成其他字段。

## 时间轴模式示例

上传至少 8 秒的视频，以下文字会分别在第 0.5–3 秒和 4–7 秒播放：

```bash
curl --fail-with-body --show-error \
  'http://192.168.31.252:18080/media/videos/dub' \
  -H "Authorization: Bearer $SUB2API_KEY" \
  -F "video=@${VIDEO_FILE};type=video/mp4" \
  --form-string 'options={"mode":"timeline","language":"zh","keep_original_audio":false,"segments":[{"start":0.5,"end":3,"text":"你好，我是曼波。"},{"start":4,"end":7,"text":"欢迎观看这个视频。"}]}' \
  -o dub-task.json
```

也可使用网页导出的 `options.json`，cURL 的 `<` 会读取文件作为文本表单字段：

```bash
curl --fail-with-body 'http://192.168.31.252:18080/media/videos/dub' \
  -H "Authorization: Bearer $SUB2API_KEY" \
  -F "video=@${VIDEO_FILE}" -F 'options=<options.json' -o dub-task.json
```

## 任务和下载

当前请求会等待配音完成才返回 JSON，不是立即返回后台任务 ID。请求可能持续数分钟。
必须检查业务 `status`，不能仅凭 HTTP 200 判断成功：

- `succeeded`：可下载 `output.path`。
- `failed`：查看 `error`，如时间越界、语音过长、无讲话、上游超时。
- `blocked`：服务尚未配置视频上游。

```json
{
  "task_id": "dub_example",
  "kind": "dubbing",
  "status": "succeeded",
  "output": {
    "path": "/media/tasks/dub_example/content",
    "bytes": 123456
  }
}
```

`dub-task.json` 是任务信息，不能当成视频。将实际 task_id 填入以下命令：

```bash
TASK_ID='替换为实际task_id'
curl --fail-with-body \
  "http://192.168.31.252:18080/media/tasks/${TASK_ID}" \
  -H "Authorization: Bearer $SUB2API_KEY"

curl --fail --show-error \
  "http://192.168.31.252:18080/media/tasks/${TASK_ID}/content" \
  -H "Authorization: Bearer $SUB2API_KEY" -o dubbed.mp4
```

查询和下载均需网关 API Key，复用生成时的 Key 即可。GET 查询和下载不重复按生成任务计费。
失败任务不能下载不完整产物。任务状态与视频存储在编排节点数据卷中，重启后可读取。

## 内网协议

构建并启动曼波 GPT-SoVITS、视频配音流水线与 edge-media 编排，发布内网端口供 252 的 `/media/*` 调用。

`252 edge-media /videos/dub → GPU edge-media /dub → edge-dub /dub`：
每一跳均为 multipart 的 video + options，内部 `/dub` 完成后返回 MP4。
内部桥接不在公网网关 allowlist 中。worker 兼容旧版原始视频字节请求，其选项为默认值。

```bash
curl --fail-with-body http://127.0.0.1:18084/dub \
  -F 'video=@input.mp4' --form-string 'options={"mode":"auto"}' -o dubbed.mp4
docker build -t edge-dub:local -f edge-media/dub/Dockerfile edge-media/dub
python3 -m pytest -q edge-media/tests
```

HTTP worker 一次处理一个任务，耗时推理放在线程池，健康检查不被阻塞。响应结束或失败后清理临时目录。
镜像预下载 faster-whisper medium（CPU int8）；GPT-SoVITS 合成运行在 GPU 推理服务。

| 环境变量 | 默认 / 用途 |
| --- | --- |
| MEDIA_TTS_UPSTREAM_URL | GPT-SoVITS 根地址，必填 |
| MEDIA_TTS_REFER_WAV / MEDIA_TTS_PROMPT_TEXT | 参考音频路径（上游可访问）及对应文字 |
| MEDIA_TTS_PROMPT_LANGUAGE | zh |
| MEDIA_TTS_TIMEOUT_SECONDS | 单段合成 180 秒 |
| MEDIA_DUB_LANGUAGE | zh |
| MEDIA_DUB_WHISPER_MODEL | 镜像 medium，独立脚本 large-v3 |
| MEDIA_DUB_WHISPER_DEVICE / MEDIA_DUB_WHISPER_COMPUTE | 镜像 cpu / int8 |
| MEDIA_MAX_UPLOAD_BYTES | 536870912 |
| MEDIA_VIDEO_TIMEOUT_SECONDS | 编排转发 3600 秒 |

命令模式仍支持 MEDIA_INPUT / MEDIA_OUTPUT / MEDIA_TASK_DIR / MEDIA_METADATA_JSON，
最后一项格式为 `{"options":{...}}`，与 HTTP 使用同一个配音实现。
