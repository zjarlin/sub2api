#!/usr/bin/env bash
# 在天津海光 DCU 机器上启动 GPT-SoVITS 推理 API。
set -euo pipefail
cd /opt/GPT-SoVITS

export CUDA_VISIBLE_DEVICES="${CUDA_VISIBLE_DEVICES:-0}"
export HIP_VISIBLE_DEVICES="${HIP_VISIBLE_DEVICES:-0}"
export LD_LIBRARY_PATH="/opt/dtk-26.04/.hyhal/rocm_smi/lib:/opt/dtk/lib:/opt/dtk/lib64:${LD_LIBRARY_PATH:-}"

GPT_WEIGHT="${GPT_SOVITS_GPT_WEIGHT:-/models/manbo/GPT_weights_v2ProPlus/manbo-e15.ckpt}"
SOVITS_WEIGHT="${GPT_SOVITS_SOVITS_WEIGHT:-/models/manbo/SoVITS_weights_v2ProPlus/manbo_e8_s904.pth}"
REFER_WAV="${GPT_SOVITS_REFER_WAV:-/models/manbo/reference/reference.mp3}"

# api.py 按工作目录相对路径找权重，先软链到期望位置。
mkdir -p GPT_weights_v2ProPlus SoVITS_weights_v2ProPlus
[ -f "$GPT_WEIGHT" ] && ln -sf "$GPT_WEIGHT" "GPT_weights_v2ProPlus/$(basename "$GPT_WEIGHT")"
[ -f "$SOVITS_WEIGHT" ] && ln -sf "$SOVITS_WEIGHT" "SoVITS_weights_v2ProPlus/$(basename "$SOVITS_WEIGHT")"

# 海光 DCU 的 torch.fft 不支持 half，默认全精度（fp32）推理。
PRECISION_FLAG="${GPT_SOVITS_PRECISION_FLAG:--fp}"

# GPT-SoVITS 参考音频只应取 3~10 秒。上游 Manbo 仓库给的是 50 多个片段拼接
# 的长音频，直接整段当参考会让 AR 模型过早输出 EOS，合成结果接近静音。
# 这里裁到设定长度并生成默认参考参数，保证 /?text=... 无参调用可直接出声。
DEFAULT_REFER_ARGS=()
if [ -f "$REFER_WAV" ]; then
  TRIMMED="/tmp/manbo_reference_trimmed.wav"
  python3 - "$REFER_WAV" "$TRIMMED" "${GPT_SOVITS_REFER_SECONDS:-6}" "${GPT_SOVITS_REFER_OFFSET:-0.12}" <<'PYTRIM'
import sys
import soundfile as sf
import numpy as np

src, dst, seconds, offset = sys.argv[1], sys.argv[2], float(sys.argv[3]), float(sys.argv[4])
data, sr = sf.read(src, dtype="float32", always_2d=True)
if data.shape[1] > 1:
    data = data.mean(axis=1, keepdims=True)
start = int(offset * sr)
stop = start + int(seconds * sr)
clip = data[start:stop]
sf.write(dst, clip, sr)
print(f"trimmed reference {src} -> {dst} ({clip.shape[0] / sr:.2f}s)")
PYTRIM
  DEFAULT_REFER_ARGS=(-dr "$TRIMMED")
fi

# 参考文本需要与裁剪后的音频内容对应，否则 AR 模型同样会早早停止。
DEFAULT_REFER_TEXT="${GPT_SOVITS_PROMPT_TEXT:-清晨的公园被一层薄薄的雾气笼罩着，柳树的枝条刚刚抽出嫩绿的新芽}"
DEFAULT_REFER_LANGUAGE="${GPT_SOVITS_PROMPT_LANGUAGE:-zh}"
if [ ${#DEFAULT_REFER_ARGS[@]} -gt 0 ]; then
  DEFAULT_REFER_ARGS+=(-dt "$DEFAULT_REFER_TEXT" -dl "$DEFAULT_REFER_LANGUAGE")
fi

exec python3 api.py \
  -a "${GPT_SOVITS_BIND:-0.0.0.0}" \
  -p "${GPT_SOVITS_PORT:-9880}" \
  -g "${GPT_SOVITS_GPT_WEIGHT:-GPT_weights_v2ProPlus/manbo-e15.ckpt}" \
  -s "${GPT_SOVITS_SOVITS_WEIGHT:-SoVITS_weights_v2ProPlus/manbo_e8_s904.pth}" \
  $PRECISION_FLAG \
  "${DEFAULT_REFER_ARGS[@]}"
