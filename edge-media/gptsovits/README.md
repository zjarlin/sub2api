# GPT-SoVITS（曼波音色）· 海光 DCU 部署

在天津海光 DCU 机器上运行 GPT-SoVITS 推理服务，提供曼波音色 TTS。
镜像基于 `hy-vllm:latest`（Ubuntu 22.04 + Python 3.10 + DTK 26.04 + 海光版
torch 2.9），**保留 DTK 自带 torch，不安装 PyPI torch/torchaudio**。

## 为什么需要这些补丁

上游 GPT-SoVITS 只考虑 CUDA/NVIDIA，直接放到海光 DCU 上有四类问题，全部
固化成可复现的文件：

1. **没有 torchaudio**：DTK 镜像缺 torchaudio，PyPI 版与 DTK torch ABI 不兼容。
   → `torchaudio-shim/torchaudio.py` 用 soundfile + librosa 实现最小子集。
2. **shim 重采样丢设备**：librosa 在 CPU 上重采样，结果没搬回原设备，导致
   `RefEncoder` 的 Linear/Conv 出现 `mat2 is on cuda:0 ... on cpu`。
   → shim 的 `resample` 保留输入设备。
3. **SV 模块设备不一致**：`sv.py` 用 CPU 上的 wav 算 fbank 再喂给 GPU 上的
   ERes2NetV2。→ `patch-gpt-sovits.py` 在 forward3 前把 feat 移到模型设备。
4. **参考音频过长**：上游曼波仓库给的是 50 多个片段拼接的长音频，整段当参考
   会让 AR 模型过早输出 EOS，合成结果接近静音。→ entrypoint 裁到 6 秒并配
   默认参考文本。
5. **NLTK 数据运行期下载挂起**：文本含英文单词时 g2p_en 会去
   `raw.githubusercontent.com` 下载语料，天津不可达，请求会卡住数分钟。
   → 镜像内预置 taggers/corpora/tokenizers 压缩包；注意 g2p_en 查找的是
   `taggers/averaged_perceptron_tagger.zip`（zip 形式），所以保留 zip 不解压。

## 构建

```bash
# 模型权重目录（宿主）
# /opt/gptsovits-models/manbo/
#   GPT_weights_v2ProPlus/manbo-e15.ckpt
#   SoVITS_weights_v2ProPlus/manbo_e8_s904.pth
#   reference/reference.mp3

docker build -t gpt-sovits:manbo -f edge-media/gptsovits/Dockerfile edge-media/gptsovits
```

权重可用 `../scripts/prepare-models.sh` 下载（走 media.githubusercontent 镜像）。

## 运行

```bash
GPT_SOVITS_IMAGE=gpt-sovits:manbo \
GPT_SOVITS_MODELS_DIR=/opt/gptsovits-models \
SUB2API_NETWORK=sub2api_sub2api-network \
  docker compose -f edge-media/gptsovits/compose/docker-compose.yml up -d
```

服务监听 9880，接入 `sub2api_sub2api-network`，供 edge-media 通过
`http://gpt-sovits:9880` 调用。

## 验证

```bash
# 无参调用走 entrypoint 注入的裁剪参考音频 + 默认参考文本
curl -fsS -G http://127.0.0.1:9880/ \
  --data-urlencode 'text=你好，我是曼波。' \
  --data-urlencode text_language=zh -o manbo.wav
ffprobe -v error -show_entries format=duration -of csv=p=0 manbo.wav
```

输出应为 32kHz 单声道 WAV，RMS 明显大于 0（约 3000 量级），时长与文本匹配。
如果出现接近静音的结果，优先检查参考音频长度与 `prompt_text` 是否对应。

## 环境变量

| 变量 | 说明 |
| --- | --- |
| `GPT_SOVITS_GPT_WEIGHT` / `GPT_SOVITS_SOVITS_WEIGHT` | GPT / SoVITS 权重路径 |
| `GPT_SOVITS_REFER_WAV` | 参考音频（会被裁到 `GPT_SOVITS_REFER_SECONDS` 秒） |
| `GPT_SOVITS_REFER_SECONDS` / `GPT_SOVITS_REFER_OFFSET` | 裁剪长度与起点，默认 6 秒 / 0.12 秒 |
| `GPT_SOVITS_PROMPT_TEXT` / `GPT_SOVITS_PROMPT_LANGUAGE` | 裁剪后音频对应的参考文本与语种 |
| `GPT_SOVITS_PRECISION_FLAG` | 默认 `-fp`（DCU 的 torch.fft 不支持 half） |
